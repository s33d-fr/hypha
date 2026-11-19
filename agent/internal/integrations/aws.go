package integrations

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── AWS (§ 46) : EC2, EBS, alarmes CloudWatch et identité, par l'API, en lecture seule ────────
//
// Signature SigV4 et API « Query » (XML) d'EC2, de CloudWatch et de STS, sans SDK. Politique IAM
// attendue : ec2:DescribeInstances, ec2:DescribeInstanceStatus, ec2:DescribeVolumes,
// cloudwatch:DescribeAlarms, sts:GetCallerIdentity (+ cloudwatch:GetMetricData si metrics).
// Les instances deviennent des ressources cloud (hôtes sans agent) ; les alarmes CloudWatch, des
// contrôles. Les mesures CPU de CloudWatch (GetMetricData) sont facturées par AWS au client :
// elles ne sont lues qu'avec « metrics: true ». Une tâche de fond par passage, comme vSphere.

type AWS struct {
	cfg    config.AWSConfig
	post   func(route string, fields map[string]any)
	pub    *resourcePublisher
	client *http.Client
	now    func() time.Time

	once sync.Once
	stop context.CancelFunc

	mu   sync.Mutex
	snap vsSnapshot
}

func newAWS(cfg config.AWSConfig, d Deps) *AWS {
	return &AWS{cfg: cfg, post: d.Post, pub: newResourcePublisher(d), client: &http.Client{Timeout: 30 * time.Second}, now: time.Now}
}

func (a *AWS) name() string          { return "aws" }
func (a *AWS) period() time.Duration { return 30 * time.Second }
func (a *AWS) close() {
	if a.stop != nil {
		a.stop()
	}
}

func (a *AWS) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if a.cfg.Enabled == "false" {
		return nil, "disabled"
	}
	if !a.cfg.Configured() {
		if a.cfg.Enabled == "true" {
			return nil, "erreur : access_key_id, secret_access_key et regions sont nécessaires"
		}
		return nil, "absent"
	}
	a.once.Do(a.start)
	a.mu.Lock()
	s := a.snap
	a.mu.Unlock()
	if s.at.IsZero() {
		return nil, "partiel : premier relevé en cours"
	}
	ts := model.Ms(now)
	if now.Sub(s.at) > 3*a.cfg.Interval.D()+time.Minute {
		return []model.Check{canConnect("aws", ts, false, "aucun relevé depuis "+now.Sub(s.at).Round(time.Second).String(), tags())}, "erreur : relevé périmé"
	}
	out := make([]model.Check, 0, len(s.checks))
	for _, c := range s.checks {
		out = append(out, model.Check{TS: ts, Name: c.name, Status: c.status, Message: c.msg, Tags: tags(c.kv...)})
	}
	for _, g := range s.gauges {
		b.Gauge(g.name, g.unit, g.v, g.kv...)
	}
	a.pub.publish(now, s.resources, "aws", "ok")
	if len(s.errs) > 0 {
		return out, fmt.Sprintf("partiel : %d région(s) en échec", len(s.errs))
	}
	return out, "ok"
}

func (a *AWS) start() {
	ctx, cancel := context.WithCancel(context.Background())
	a.stop = cancel
	go func() {
		for {
			pctx, c := context.WithTimeout(ctx, 3*time.Minute)
			s := a.poll(pctx)
			c()
			a.mu.Lock()
			a.snap = s
			a.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(a.cfg.Interval.D()):
			}
		}
	}()
}

// ── signature et appels ─────────────────────────────────────────────────────────────────────

func hmacSHA256(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

func sha256Hex(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// sigv4 signe une requête (AWS Signature Version 4) : en-têtes host, x-amz-date, content-type
// s'il est posé, x-amz-security-token s'il y a un jeton de session.
func sigv4(req *http.Request, body []byte, ak, sk, token, region, service string, t time.Time) {
	amzDate := t.UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	req.Header.Set("X-Amz-Date", amzDate)
	if token != "" {
		req.Header.Set("X-Amz-Security-Token", token)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	hdr := map[string]string{"host": host, "x-amz-date": amzDate}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		hdr["content-type"] = ct
	}
	if token != "" {
		hdr["x-amz-security-token"] = token
	}
	names := make([]string, 0, len(hdr))
	for k := range hdr {
		names = append(names, k)
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, k := range names {
		ch.WriteString(k + ":" + strings.TrimSpace(hdr[k]) + "\n")
	}
	signed := strings.Join(names, ";")
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	q := req.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var cq []string
	for _, k := range keys {
		vs := append([]string{}, q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			cq = append(cq, awsEscape(k)+"="+awsEscape(v))
		}
	}
	creq := strings.Join([]string{req.Method, path, strings.Join(cq, "&"), ch.String(), signed, sha256Hex(body)}, "\n")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(creq))
	k := hmacSHA256([]byte("AWS4"+sk), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "aws4_request")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+ak+"/"+scope+", SignedHeaders="+signed+", Signature="+hex.EncodeToString(hmacSHA256(k, sts)))
}

// awsEscape : encodage RFC 3986 que demande SigV4 (espace en %20, « ~ » laissé tel quel).
func awsEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

type awsError struct {
	Code    string `xml:"Errors>Error>Code"`
	Message string `xml:"Errors>Error>Message"`
	Code2   string `xml:"Error>Code"`
	Msg2    string `xml:"Error>Message"`
}

// call : une action de l'API Query (POST de formulaire), réponse XML décodée dans out.
func (a *AWS) call(ctx context.Context, service, region, version string, params url.Values, out any) error {
	raw, err := a.callRaw(ctx, service, region, version, params)
	if err != nil {
		return err
	}
	return xml.Unmarshal(raw, out)
}

func (a *AWS) callRaw(ctx context.Context, service, region, version string, params url.Values) ([]byte, error) {
	params.Set("Version", version)
	body := []byte(params.Encode())
	endpoint := strings.TrimRight(a.cfg.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://" + service + "." + region + ".amazonaws.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req.Header.Set("User-Agent", "hypha-agent")
	sigv4(req, body, a.cfg.AccessKeyID, a.cfg.SecretAccessKey, a.cfg.SessionToken, region, service, a.now())
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	action := params.Get("Action")
	if resp.StatusCode != http.StatusOK {
		var e awsError
		_ = xml.Unmarshal(raw, &e)
		code, msg := firstNonEmpty(e.Code, e.Code2), firstNonEmpty(e.Message, e.Msg2)
		if code == "" {
			return nil, fmt.Errorf("%s %s : HTTP %d", service, action, resp.StatusCode)
		}
		return nil, fmt.Errorf("%s %s : %s (%s)", service, action, code, model.Truncate(msg, 200))
	}
	return raw, nil
}

// ── relevé ──────────────────────────────────────────────────────────────────────────────────

type awsTag struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

type ec2Instance struct {
	ID        string   `xml:"instanceId"`
	Type      string   `xml:"instanceType"`
	State     string   `xml:"instanceState>name"`
	AZ        string   `xml:"placement>availabilityZone"`
	PrivateIP string   `xml:"privateIpAddress"`
	PublicIP  string   `xml:"ipAddress"`
	Launch    string   `xml:"launchTime"`
	Platform  string   `xml:"platformDetails"`
	Tags      []awsTag `xml:"tagSet>item"`
}

type ec2Status struct {
	ID       string `xml:"instanceId"`
	System   string `xml:"systemStatus>status"`
	Instance string `xml:"instanceStatus>status"`
	Events   []struct {
		Code        string `xml:"code"`
		Description string `xml:"description"`
		NotBefore   string `xml:"notBefore"`
	} `xml:"eventsSet>item"`
}

type ebsVolume struct {
	ID     string `xml:"volumeId"`
	Size   string `xml:"size"`
	Status string `xml:"status"`
	AZ     string `xml:"availabilityZone"`
	Tags   []awsTag `xml:"tagSet>item"`
}

type cwAlarm struct {
	Name      string `xml:"AlarmName"`
	State     string `xml:"StateValue"`
	Reason    string `xml:"StateReason"`
	Metric    string `xml:"MetricName"`
	Namespace string `xml:"Namespace"`
}

func tagValue(tags []awsTag, k string) string {
	for _, t := range tags {
		if t.Key == k {
			return t.Value
		}
	}
	return ""
}

func (a *AWS) poll(ctx context.Context) vsSnapshot {
	start := time.Now()
	var s vsSnapshot
	regions := append([]string{}, a.cfg.Regions...)
	var id struct {
		Account string `xml:"GetCallerIdentityResult>Account"`
		Arn     string `xml:"GetCallerIdentityResult>Arn"`
	}
	if err := a.call(ctx, "sts", regions[0], "2011-06-15", url.Values{"Action": {"GetCallerIdentity"}}, &id); err != nil {
		s.errs = append(s.errs, err.Error())
		s.checks = append(s.checks, ovhCheck{"aws.can_connect", model.CheckCrit, err.Error(), nil})
		s.at, s.took = time.Now(), time.Since(start)
		return s
	}
	account := id.Account
	instances := 0
	for _, region := range regions {
		n, err := a.pollRegion(ctx, account, region, &s)
		instances += n
		if err != nil {
			s.errs = append(s.errs, region+" : "+err.Error())
		}
	}
	conn := ovhCheck{"aws.can_connect", model.CheckOK, fmt.Sprintf("compte %s (%s) : %d instance(s) dans %d région(s)", account, id.Arn, instances, len(regions)),
		[]string{"aws_account", account}}
	if len(s.errs) > 0 {
		conn.status, conn.msg = model.CheckWarn, conn.msg+" ; lecture partielle : "+model.Truncate(strings.Join(s.errs, " ; "), 300)
	}
	s.checks = append(s.checks, conn)
	s.at, s.took = time.Now(), time.Since(start)
	return s
}

// paged : appels successifs tant que l'API rend un jeton de page (nextToken ou NextToken).
func (a *AWS) paged(ctx context.Context, service, region, version string, params url.Values, tokenParam string, each func([]byte) (string, error)) error {
	token := ""
	for page := 0; page < 50; page++ {
		p := url.Values{}
		for k, v := range params {
			p[k] = v
		}
		if token != "" {
			p.Set(tokenParam, token)
		}
		raw, err := a.callRaw(ctx, service, region, version, p)
		if err != nil {
			return err
		}
		next, err := each(raw)
		if err != nil || next == "" {
			return err
		}
		token = next
	}
	return nil
}

func (a *AWS) pollRegion(ctx context.Context, account, region string, s *vsSnapshot) (int, error) {
	const ec2v, cwv = "2016-11-15", "2010-08-01"
	var insts []ec2Instance
	err := a.paged(ctx, "ec2", region, ec2v, url.Values{"Action": {"DescribeInstances"}, "MaxResults": {"500"}}, "NextToken", func(b []byte) (string, error) {
		var r struct {
			Res []struct {
				Instances []ec2Instance `xml:"instancesSet>item"`
			} `xml:"reservationSet>item"`
			Next string `xml:"nextToken"`
		}
		if err := xml.Unmarshal(b, &r); err != nil {
			return "", err
		}
		for _, x := range r.Res {
			insts = append(insts, x.Instances...)
		}
		return r.Next, nil
	})
	if err != nil {
		return 0, err
	}
	status := map[string]ec2Status{}
	var problems []string
	if err := a.paged(ctx, "ec2", region, ec2v, url.Values{"Action": {"DescribeInstanceStatus"}, "MaxResults": {"1000"}}, "NextToken", func(b []byte) (string, error) {
		var r struct {
			Items []ec2Status `xml:"instanceStatusSet>item"`
			Next  string      `xml:"nextToken"`
		}
		if err := xml.Unmarshal(b, &r); err != nil {
			return "", err
		}
		for _, it := range r.Items {
			status[it.ID] = it
		}
		return r.Next, nil
	}); err != nil {
		problems = append(problems, err.Error())
	}
	cpu := map[string]float64{}
	if a.cfg.Metrics {
		if err := a.cpuMetrics(ctx, region, insts, cpu); err != nil {
			problems = append(problems, err.Error())
		}
	}
	sort.Slice(insts, func(i, j int) bool { return insts[i].ID < insts[j].ID })
	n := 0
	for _, in := range insts {
		if in.State == "terminated" || in.State == "shutting-down" {
			continue
		}
		n++
		name := firstNonEmpty(tagValue(in.Tags, "Name"), in.ID)
		kv := []string{"aws_region", region, "aws_instance", in.ID}
		tags := map[string]string{"provider": "aws", "aws_account": account, "aws_region": region}
		for k, v := range a.cfg.Tags {
			tags[k] = v
		}
		r := &cloudResource{key: "aws:" + account + ":" + region + ":" + in.ID, name: name, tags: tags,
			device: model.Device{Provider: "aws", Type: "ec2_instance", Service: account, DisplayName: name, IP: firstNonEmpty(in.PrivateIP, in.PublicIP),
				Region: in.AZ, PowerState: in.State, Model: in.Type, Vendor: "Amazon Web Services"}}
		r.platform, r.version = "AWS EC2", in.Platform
		for _, ip := range []string{in.PrivateIP, in.PublicIP} {
			if ip != "" {
				r.ips = append(r.ips, ip)
			}
		}
		if t, err := time.Parse(time.RFC3339, in.Launch); err == nil && in.State == "running" {
			r.bootTime = t.UnixMilli()
		}
		st, msg := model.CheckOK, fmt.Sprintf("%s (%s) : %s", name, in.Type, in.State)
		if x, ok := status[in.ID]; ok {
			switch {
			case x.System == "impaired" || x.Instance == "impaired":
				st, msg = model.CheckCrit, msg+fmt.Sprintf(", contrôles d'état en échec (système %s, instance %s)", x.System, x.Instance)
			case x.System == "ok" && x.Instance == "ok":
				msg += ", contrôles d'état réussis"
			}
			var evs []string
			for _, e := range x.Events {
				if strings.HasPrefix(e.Description, "[Completed]") || strings.HasPrefix(e.Description, "[Canceled]") {
					continue
				}
				evs = append(evs, e.Code+" à partir du "+e.NotBefore)
			}
			if len(evs) > 0 {
				msg += " ; événement planifié par AWS : " + strings.Join(evs, ", ")
				if st == model.CheckOK {
					st = model.CheckWarn
				}
			}
		}
		r.checks = append(r.checks, ovhCheck{"aws.ec2.status", st, msg, kv})
		if v, ok := cpu[in.ID]; ok {
			r.gauges = append(r.gauges, ovhGauge{name: "aws.ec2.cpu.usage", unit: "percent", v: round1(v), kv: kv})
		}
		s.resources = append(s.resources, r)
	}
	s.gauges = append(s.gauges, ovhGauge{name: "aws.ec2.instances", unit: "count", v: float64(n), kv: []string{"aws_region", region}})

	if err := a.paged(ctx, "ec2", region, ec2v, url.Values{"Action": {"DescribeVolumes"}, "MaxResults": {"500"}}, "NextToken", func(b []byte) (string, error) {
		var r struct {
			Items []ebsVolume `xml:"volumeSet>item"`
			Next  string      `xml:"nextToken"`
		}
		if err := xml.Unmarshal(b, &r); err != nil {
			return "", err
		}
		for _, v := range r.Items {
			kv := []string{"aws_region", region, "aws_volume", v.ID}
			name := firstNonEmpty(tagValue(v.Tags, "Name"), v.ID)
			st := model.CheckOK
			if v.Status == "error" {
				st = model.CheckCrit
			}
			s.checks = append(s.checks, ovhCheck{"aws.ebs.volume", st, fmt.Sprintf("volume %s (%s Gio, %s) : %s", name, v.Size, v.AZ, v.Status), kv})
		}
		return r.Next, nil
	}); err != nil {
		problems = append(problems, err.Error())
	}

	if err := a.paged(ctx, "monitoring", region, cwv, url.Values{"Action": {"DescribeAlarms"}, "AlarmTypes.member.1": {"MetricAlarm"}, "MaxRecords": {"100"}}, "NextToken", func(b []byte) (string, error) {
		var r struct {
			Alarms []cwAlarm `xml:"DescribeAlarmsResult>MetricAlarms>member"`
			Next   string    `xml:"DescribeAlarmsResult>NextToken"`
		}
		if err := xml.Unmarshal(b, &r); err != nil {
			return "", err
		}
		for _, al := range r.Alarms {
			kv := []string{"aws_region", region, "aws_alarm", al.Name}
			st, msg := model.CheckOK, "alarme "+al.Name+" : OK"
			switch al.State {
			case "ALARM":
				st, msg = model.CheckCrit, "alarme "+al.Name+" déclenchée : "+model.Truncate(al.Reason, 300)
			case "INSUFFICIENT_DATA":
				msg = "alarme " + al.Name + " : données insuffisantes"
			}
			if al.Metric != "" {
				msg += " (" + al.Namespace + "/" + al.Metric + ")"
			}
			s.checks = append(s.checks, ovhCheck{"aws.cloudwatch.alarm", st, msg, kv})
		}
		return r.Next, nil
	}); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return n, fmt.Errorf("%s", strings.Join(problems, " ; "))
	}
	return n, nil
}

// cpuMetrics : CPU moyen des 10 dernières minutes de chaque instance en marche (GetMetricData,
// facturé par AWS), 100 instances par appel.
func (a *AWS) cpuMetrics(ctx context.Context, region string, insts []ec2Instance, out map[string]float64) error {
	var ids []string
	for _, in := range insts {
		if in.State == "running" {
			ids = append(ids, in.ID)
		}
	}
	end := a.now().UTC().Truncate(time.Minute)
	for i := 0; i < len(ids); i += 100 {
		batch := ids[i:min(i+100, len(ids))]
		p := url.Values{"Action": {"GetMetricData"}, "StartTime": {end.Add(-10 * time.Minute).Format(time.RFC3339)}, "EndTime": {end.Format(time.RFC3339)},
			"ScanBy": {"TimestampDescending"}}
		for j, id := range batch {
			m := "MetricDataQueries.member." + strconv.Itoa(j+1)
			p.Set(m+".Id", "q"+strconv.Itoa(j))
			p.Set(m+".MetricStat.Metric.Namespace", "AWS/EC2")
			p.Set(m+".MetricStat.Metric.MetricName", "CPUUtilization")
			p.Set(m+".MetricStat.Metric.Dimensions.member.1.Name", "InstanceId")
			p.Set(m+".MetricStat.Metric.Dimensions.member.1.Value", id)
			p.Set(m+".MetricStat.Period", "300")
			p.Set(m+".MetricStat.Stat", "Average")
		}
		var r struct {
			Results []struct {
				ID     string    `xml:"Id"`
				Values []float64 `xml:"Values>member"`
			} `xml:"GetMetricDataResult>MetricDataResults>member"`
		}
		if err := a.call(ctx, "monitoring", region, "2010-08-01", p, &r); err != nil {
			return err
		}
		for _, res := range r.Results {
			k, err := strconv.Atoi(strings.TrimPrefix(res.ID, "q"))
			if err == nil && k < len(batch) && len(res.Values) > 0 {
				out[batch[k]] = res.Values[0]
			}
		}
	}
	return nil
}

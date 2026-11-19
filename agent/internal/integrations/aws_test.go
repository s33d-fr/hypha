package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Vecteur « get-vanilla » de la suite de tests SigV4 d'AWS.
func TestSigV4Vanilla(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	at, _ := time.Parse("20060102T150405Z", "20150830T123600Z")
	sigv4(req, nil, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "", "us-east-1", "service", at)
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, " +
		"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("signature :\n %s\nattendu :\n %s", got, want)
	}
}

func TestAWSPoll(t *testing.T) {
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDESSAI/") || !strings.Contains(auth, "/eu-west-3/") {
			w.WriteHeader(403)
			fmt.Fprint(w, `<ErrorResponse><Error><Code>SignatureDoesNotMatch</Code><Message>mauvaise signature</Message></Error></ErrorResponse>`)
			return
		}
		a := r.Form.Get("Action")
		actions = append(actions, a)
		switch a {
		case "GetCallerIdentity":
			fmt.Fprint(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult>
				<Arn>arn:aws:iam::123456789012:user/hypha</Arn><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`)
		case "DescribeInstances":
			if r.Form.Get("NextToken") == "" {
				fmt.Fprint(w, `<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><reservationSet><item><instancesSet>
					<item><instanceId>i-1</instanceId><instanceType>t3.micro</instanceType><instanceState><code>16</code><name>running</name></instanceState>
					<placement><availabilityZone>eu-west-3a</availabilityZone></placement><privateIpAddress>172.31.0.5</privateIpAddress>
					<launchTime>2026-11-01T10:00:00.000Z</launchTime><platformDetails>Linux/UNIX</platformDetails>
					<tagSet><item><key>Name</key><value>web</value></item></tagSet></item>
					<item><instanceId>i-2</instanceId><instanceType>t3.small</instanceType><instanceState><name>running</name></instanceState></item>
				</instancesSet></item></reservationSet><nextToken>page2</nextToken></DescribeInstancesResponse>`)
				return
			}
			fmt.Fprint(w, `<DescribeInstancesResponse><reservationSet><item><instancesSet>
				<item><instanceId>i-3</instanceId><instanceType>t3.micro</instanceType><instanceState><name>stopped</name></instanceState></item>
				<item><instanceId>i-4</instanceId><instanceType>t3.micro</instanceType><instanceState><name>terminated</name></instanceState></item>
			</instancesSet></item></reservationSet></DescribeInstancesResponse>`)
		case "DescribeInstanceStatus":
			fmt.Fprint(w, `<DescribeInstanceStatusResponse><instanceStatusSet>
				<item><instanceId>i-1</instanceId><systemStatus><status>ok</status></systemStatus><instanceStatus><status>ok</status></instanceStatus>
				 <eventsSet><item><code>system-reboot</code><description>scheduled reboot</description><notBefore>2026-11-25T02:00:00.000Z</notBefore></item></eventsSet></item>
				<item><instanceId>i-2</instanceId><systemStatus><status>impaired</status></systemStatus><instanceStatus><status>ok</status></instanceStatus></item>
			</instanceStatusSet></DescribeInstanceStatusResponse>`)
		case "DescribeVolumes":
			fmt.Fprint(w, `<DescribeVolumesResponse><volumeSet>
				<item><volumeId>vol-1</volumeId><size>8</size><status>in-use</status><availabilityZone>eu-west-3a</availabilityZone></item>
				<item><volumeId>vol-2</volumeId><size>20</size><status>error</status><availabilityZone>eu-west-3a</availabilityZone></item>
			</volumeSet></DescribeVolumesResponse>`)
		case "DescribeAlarms":
			fmt.Fprint(w, `<DescribeAlarmsResponse xmlns="http://monitoring.amazonaws.com/doc/2010-08-01/"><DescribeAlarmsResult><MetricAlarms>
				<member><AlarmName>cpu-web</AlarmName><StateValue>ALARM</StateValue><StateReason>Threshold Crossed: 1 datapoint [95.0] was greater than 90.0</StateReason>
				 <MetricName>CPUUtilization</MetricName><Namespace>AWS/EC2</Namespace></member>
				<member><AlarmName>disque</AlarmName><StateValue>OK</StateValue></member>
			</MetricAlarms></DescribeAlarmsResult></DescribeAlarmsResponse>`)
		case "GetMetricData":
			fmt.Fprint(w, `<GetMetricDataResponse><GetMetricDataResult><MetricDataResults>
				<member><Id>q0</Id><Values><member>42.25</member><member>40</member></Values></member>
			</MetricDataResults></GetMetricDataResult></GetMetricDataResponse>`)
		default:
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	a := newAWS(config.AWSConfig{Enabled: "auto", AccessKeyID: "AKIDESSAI", SecretAccessKey: "secret", Regions: []string{"eu-west-3"},
		Endpoint: srv.URL, Metrics: true, Interval: config.Duration(5 * time.Minute)}, Deps{})
	s := a.poll(context.Background())
	if len(s.errs) != 0 {
		t.Fatalf("erreurs : %v", s.errs)
	}
	checks := map[string]ovhCheck{}
	for _, c := range s.checks {
		checks[c.name+"/"+strings.Join(c.kv, ",")] = c
	}
	for _, r := range s.resources {
		for _, c := range r.checks {
			checks[c.name+"/"+r.name] = c
		}
	}
	for key, want := range map[string]struct {
		st   int
		text string
	}{
		"aws.can_connect/aws_account,123456789012":                 {model.CheckOK, "compte 123456789012 (arn:aws:iam::123456789012:user/hypha) : 3 instance(s) dans 1 région(s)"},
		"aws.ec2.status/web":                                       {model.CheckWarn, "événement planifié par AWS : system-reboot à partir du 2026-11-25T02:00:00.000Z"},
		"aws.ec2.status/i-2":                                       {model.CheckCrit, "contrôles d'état en échec (système impaired"},
		"aws.ec2.status/i-3":                                       {model.CheckOK, "stopped"},
		"aws.ebs.volume/aws_region,eu-west-3,aws_volume,vol-2":     {model.CheckCrit, "error"},
		"aws.cloudwatch.alarm/aws_region,eu-west-3,aws_alarm,cpu-web": {model.CheckCrit, "déclenchée : Threshold Crossed"},
		"aws.cloudwatch.alarm/aws_region,eu-west-3,aws_alarm,disque":  {model.CheckOK, "OK"},
	} {
		c, ok := checks[key]
		if !ok || c.status != want.st || !strings.Contains(c.msg, want.text) {
			t.Fatalf("%s : %+v (attendu %d %q) ; %v", key, c, want.st, want.text, checks)
		}
	}
	if len(s.resources) != 3 {
		t.Fatalf("ressources : %d (l'instance terminée ne doit pas compter)", len(s.resources))
	}
	web := s.resources[0]
	if web.name != "web" || web.device.Region != "eu-west-3a" || web.device.IP != "172.31.0.5" || len(web.gauges) != 1 || web.gauges[0].v != 42.3 {
		t.Fatalf("ressource web : %+v", web)
	}
	if strings.Join(actions, ",") != "GetCallerIdentity,DescribeInstances,DescribeInstances,DescribeInstanceStatus,GetMetricData,DescribeVolumes,DescribeAlarms" {
		t.Fatalf("appels : %v", actions)
	}
	// Mauvaise signature : CRIT, message d'AWS repris, sans le secret.
	a = newAWS(config.AWSConfig{Enabled: "auto", AccessKeyID: "AUTRE", SecretAccessKey: "secret-a-ne-pas-voir", Regions: []string{"eu-west-3"},
		Endpoint: srv.URL, Interval: config.Duration(5 * time.Minute)}, Deps{})
	s = a.poll(context.Background())
	if len(s.checks) != 1 || s.checks[0].status != model.CheckCrit || !strings.Contains(s.checks[0].msg, "SignatureDoesNotMatch") ||
		strings.Contains(s.checks[0].msg, "secret-a-ne-pas-voir") {
		t.Fatalf("refus : %+v", s.checks)
	}
}

// Essai contre une API compatible (LocalStack) ou un vrai compte, lancé à la main : AWS_REEL=1,
// AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION, AWS_ENDPOINT (vide = AWS).
func TestAWSReel(t *testing.T) {
	if os.Getenv("AWS_REEL") != "1" {
		t.Skip("AWS_REEL=1 pour interroger une API AWS")
	}
	a := newAWS(config.AWSConfig{Enabled: "true", AccessKeyID: os.Getenv("AWS_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		Regions: []string{os.Getenv("AWS_REGION")}, Endpoint: os.Getenv("AWS_ENDPOINT"), Metrics: os.Getenv("AWS_METRICS") == "1",
		Interval: config.Duration(5 * time.Minute)}, Deps{})
	s := a.poll(context.Background())
	t.Logf("relevé en %s, erreurs : %v", s.took, s.errs)
	for _, c := range s.checks {
		t.Logf("[%d] %s : %s", c.status, c.name, c.msg)
	}
	for _, r := range s.resources {
		for _, c := range r.checks {
			t.Logf("ressource %s [%d] %s : %s", r.name, c.status, c.name, c.msg)
		}
	}
}

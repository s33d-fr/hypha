import hashlib
import json

from hypha import util


def test_tags_hash_canonical_form():
    tags = {"host": "web-01", "device": "sda"}
    expected = hashlib.sha256(json.dumps(tags, sort_keys=True, separators=(",", ":")).encode()).digest()
    assert util.tags_hash(tags) == expected
    assert util.tags_hash({"device": "sda", "host": "web-01"}) == expected


def test_clean_tags_keeps_valid_only():
    raw = {"host": "web-01", "Env": "prod", "bad key": "x", "n": 3, "f": 2.0, "b": True, "long": "x" * 300,
           "empty": "", "none": None, "path/a.b-c_d": "ok"}
    tags = util.clean_tags(raw)
    assert tags["host"] == "web-01"
    assert "Env" not in tags and "bad key" not in tags
    assert tags["n"] == "3" and tags["f"] == "2" and tags["b"] == "true"
    assert len(tags["long"]) == 200
    assert "empty" not in tags and "none" not in tags
    assert tags["path/a.b-c_d"] == "ok"


def test_clean_tags_normalized_keys():
    assert util.clean_tags({"HTTP Method": "GET"}, normalize_keys=True) == {"http_method": "GET"}


def test_clean_text_removes_nul_and_surrogates():
    assert util.clean_text("a\x00b") == "ab"
    assert util.clean_text("x\ud800y") == "x?y"
    assert util.clean_text("abcdef", 3) == "abc"


def test_clean_json_nan_and_depth():
    out = util.clean_json({"a": float("nan"), "b": [1, float("inf")], "c": "z\x00"})
    assert out == {"a": None, "b": [1, None], "c": "z"}
    assert json.loads(util.jdump({"x": float("nan")})) == {"x": None}


def test_api_key_format():
    key = util.generate_api_key()
    assert util.KEY_RE.match(key)
    assert len(key) == 44 and util.key_prefix(key) == key[:12]
    assert util.key_hash(key) == hashlib.sha256(key.encode()).digest()


def test_bucket_floor_and_nice_step():
    origin = util.dt_to_ms(util.EPOCH_2000)
    assert util.bucket_floor(origin + 61_000, 60) == origin + 60_000
    assert util.nice_step(14) == 15
    assert util.nice_step(61) == 120
    assert util.nice_step(10_000_000) == 10_000_000


def test_group_key_roundtrip():
    key = util.group_key(["host", "device"], {"host": "web-01", "device": None})
    assert key == "host:web-01,device:N/A"
    assert util.group_key([], {}) == "*"
    assert util.parse_group_key("host:web-01,mount:/var") == {"host": "web-01", "mount": "/var"}
    assert util.parse_group_key("*") == {}


def test_ttl_cache_and_rate_limiter():
    c = util.TTLCache(ttl=60, max_items=2)
    c.set("a", 1)
    assert c.get("a") == 1 and "a" in c
    c.set("b", 2, ttl=-1)
    assert c.get("b") is None
    rl = util.RateLimiter(2)
    assert rl.allow("k") and rl.allow("k") and not rl.allow("k")
    assert util.RateLimiter(0).allow("x")

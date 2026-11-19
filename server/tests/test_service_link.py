from hypha.monitors import service_link as S


def test_monitor_service_order():
    assert S.monitor_service({"tags": {"service": "api"}, "query": {"q": "service:web"}}) == "api"
    assert S.monitor_service({"tags": {}, "query": {}}, "host:web-01,service:nginx") == "nginx"
    assert S.monitor_service({"tags": {}, "query": {"service": "hypha-api"}}) == "hypha-api"
    assert S.monitor_service({"tags": {}, "query": {"q": "status:error service:nginx host:web-01"}}) == "nginx"
    assert S.monitor_service({"tags": {}, "query": {"q": "avg:trace.hits{service:api,env:prod}"}}) == "api"
    # Une exclusion ne désigne pas le service du moniteur.
    assert S.monitor_service({"tags": {}, "query": {"q": "avg:x{!service:api}"}}) is None
    assert S.monitor_service({"tags": {}, "query": {"q": "-service:api status:error"}}) is None
    assert S.monitor_service({"tags": None, "query": None}, "host:N/A,service:N/A") is None


def test_with_runbook():
    assert S.with_runbook("Disque plein", "https://wiki/runbook") == "Disque plein\n\nRunbook : https://wiki/runbook"
    assert S.with_runbook("voir https://wiki/runbook", "https://wiki/runbook") == "voir https://wiki/runbook"
    assert S.with_runbook("texte", None) == "texte"

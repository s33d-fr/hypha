package integrations

// Sorties réelles du script de la sonde WSUS sur une machine d'essai (Windows Server 2025, rôle WSUS avec la
// base interne WID, 21/11/2026) : juste après l'installation (jamais synchronisé), puis pendant la
// première synchronisation avec Microsoft Update.
const (
	wsusJSONAvant   = `{"errors":[],"version":"10.0.26100.1150","status":{"computers":0,"computers_needing":0,"computers_errors":0,"not_approved":0,"critical_not_approved":0,"needing_files":0,"approved":0,"declined":0,"expired":0,"total":0,"client_errors":0,"server_errors":0},"sync_status":"NotProcessing","sync_auto":false,"success_age_s":-1,"stale":0,"database":{"wid":true,"server":"MICROSOFT##WID","name":"SUSDB","size":10682368},"content_path":"C:\\WSUS\\WsusContent","content_free":69969141760,"pool":"Started"}`
	wsusJSONEnCours = `{"errors":[],"version":"10.0.26100.1150","status":{"computers":0,"computers_needing":0,"computers_errors":0,"not_approved":0,"critical_not_approved":0,"needing_files":0,"approved":0,"declined":0,"expired":0,"total":0,"client_errors":0,"server_errors":0},"sync_status":"Running","sync_auto":false,"success_age_s":-1,"stale":0,"database":{"wid":true,"server":"MICROSOFT##WID","name":"SUSDB","size":20119552},"content_path":"C:\\WSUS\\WsusContent","content_free":69931106304,"pool":"Started"}`
)

// Synchronisation en échec provoquée (mandataire inexistant), message de WSUS tronqué. success_age_s
// vaut -1 : la première version du script lisait LastSynchronizationTime, qui date la dernière
// tentative (16 s ici), pas la dernière réussite ; l'historique des synchronisations n'en a aucune.
const wsusJSONEchec = `{"errors":[],"version":"10.0.26100.1150","status":{"computers":0,"computers_needing":0,"computers_errors":0,"not_approved":0,"critical_not_approved":0,"needing_files":0,"approved":0,"declined":0,"expired":0,"total":0,"client_errors":0,"server_errors":0},"sync_status":"NotProcessing","sync_auto":false,"success_age_s":-1,"last_sync":{"result":"Failed","error":"UssCommunicationError","error_text":"WebException: Le nom distant n\u0027a pas pu \u00eatre r\u00e9solu: \u0027proxy-inexistant.essai.local\u0027\r\n\u00e0 System.Net.HttpWebRequest.GetRequestStream(TransportContext\u0026 context)\n   ","age_s":16,"duration_s":0},"stale":0,"database":{"wid":true,"server":"MICROSOFT##WID","name":"SUSDB","size":33751040},"content_path":"C:\\WSUS\\WsusContent","content_free":69889376256,"pool":"Started"}`

// Après une synchronisation réussie : même forme, construite sur les champs ci-dessus et ceux de
// GetLastSynchronizationInfo() (Result, Error, ErrorText, StartTime, EndTime).
const wsusJSONEssai = `{"errors":[],"version":"10.0.26100.1150","status":{"computers":1,"computers_needing":0,"computers_errors":0,"not_approved":12,"critical_not_approved":0,"needing_files":0,"approved":0,"declined":0,"expired":0,"total":12,"client_errors":0,"server_errors":0},"sync_status":"NotProcessing","sync_auto":false,"success_age_s":600,"last_sync":{"result":"Succeeded","error":"NotApplicable","error_text":"","age_s":600,"duration_s":120},"stale":1,"database":{"wid":true,"server":"MICROSOFT##WID","name":"SUSDB","size":10682368},"content_path":"C:\\WSUS\\WsusContent","content_free":69969141760,"pool":"Started"}`

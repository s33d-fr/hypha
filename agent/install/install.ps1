# Installation de hypha-agent sous Windows (contrat HYPHA § 5), PowerShell administrateur.
#
#   $env:HYPHA_API_KEY='hph_…'; iwr -useb https://hypha.s33d.fr/install/install.ps1 | iex
#   Désinstallation : $env:HYPHA_UNINSTALL='1' puis la même commande,
#   ou : & ([scriptblock]::Create((iwr -useb https://hypha.s33d.fr/install/install.ps1))) -Uninstall
#
# Strictement non interactif (lancé aussi par Ansible) : aucune question, erreur fatale
# (code de sortie non nul) sur échec, dernière ligne lisible (« hypha-agent 0.1.0 actif »).
# Rejouable : une réinstallation de la même version ne redémarre pas le service.
#
# Variables :
#   HYPHA_API_KEY     clé d'ingestion (obligatoire à la première installation)
#   HYPHA_URL         serveur (défaut https://hypha.s33d.fr ; auto-hébergé : son adresse)
#   HYPHA_TAGS        étiquettes, « env:prod,role:web » ou JSON {"env":"prod"}
#   HYPHA_HOSTNAME    nom de l'hôte dans HYPHA (défaut : nom système)
#   HYPHA_FORCE_CONF  1 = réécrire api_key et url d'une conf existante (rien d'autre)
#   HYPHA_TLS_PIN     serveur auto-hébergé à certificat autosigné ou d'autorité interne :
#                     empreinte « sha256//… » de sa clé publique (Réglages › Installer l'agent).
#                     Les téléchargements la vérifient (curl.exe --pinnedpubkey), la conf la
#                     garde (tls_pin). Windows Server 2019, Windows 10 1803 et suivants.
# HYPHA_TAGS et HYPHA_HOSTNAME ne sont écrits qu'à la création de la conf.
param([switch]$Uninstall)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue' # la barre de progression ralentit fortement Invoke-WebRequest
Set-StrictMode -Version 2

function Get-EnvValue([string]$Name) { [Environment]::GetEnvironmentVariable($Name) }
function Stop-Install([string]$Message) { throw "hypha-agent : ERREUR : $Message" }
function Write-Info([string]$Message) { Write-Output "hypha-agent : $Message" }

$ServiceName = 'HyphaAgent'
$BinDir = Join-Path $env:ProgramFiles 'Hypha'
$Exe = Join-Path $BinDir 'hypha-agent.exe'
$DataDir = Join-Path $env:ProgramData 'Hypha'
$Conf = Join-Path $DataDir 'hypha.yaml'
$Url = Get-EnvValue 'HYPHA_URL'
$UrlGiven = -not [string]::IsNullOrWhiteSpace($Url)
if (-not $UrlGiven) { $Url = 'https://hypha.s33d.fr' }
$Url = $Url.Trim().TrimEnd('/')
$Utf8NoBom = New-Object System.Text.UTF8Encoding $false

$principal = New-Object Security.Principal.WindowsPrincipal ([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Stop-Install 'lancer PowerShell en administrateur'
}
# PowerShell 5.1 sur un Windows ancien ne propose pas TLS 1.2 d'office.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# Serveur à certificat autosigné ou d'autorité interne : la clé épinglée se vérifie par curl.exe
# (--pinnedpubkey, Windows Server 2019, Windows 10 1803 et suivants). Le rappel de validation de
# .NET ne convient pas : il s'exécute hors du fil de PowerShell (WinRM, Ansible) et y échoue.
$Pin = Get-EnvValue 'HYPHA_TLS_PIN'
if (-not [string]::IsNullOrWhiteSpace($Pin)) {
    $Pin = $Pin.Trim()
    if ($Pin -cnotmatch '^sha256//[A-Za-z0-9+/]{43}=(;sha256//[A-Za-z0-9+/]{43}=)*$') {
        Stop-Install "HYPHA_TLS_PIN invalide : « $Pin » (attendu sha256//<empreinte base64>)"
    }
    if (-not (Get-Command curl.exe -ErrorAction SilentlyContinue)) {
        Stop-Install 'HYPHA_TLS_PIN demande curl.exe (Windows Server 2019, Windows 10 1803 ou plus récent)'
    }
} else {
    $Pin = ''
}

function Get-File([string]$Uri, [string]$OutFile) {
    if ($Pin) {
        & curl.exe -fsSLk --pinnedpubkey $Pin --retry 3 --connect-timeout 15 -o $OutFile $Uri
        if ($LASTEXITCODE -ne 0) { throw "curl.exe, code $LASTEXITCODE (90 : la clé du serveur n'est pas celle de HYPHA_TLS_PIN)" }
    } else {
        Invoke-WebRequest -UseBasicParsing -Uri $Uri -OutFile $OutFile
    }
}

# Lance hypha-agent.exe et rend son code de sortie (sa sortie standard est rendue au besoin).
function Invoke-Agent([string]$Path, [string[]]$Arguments, [switch]$Quiet) {
    if ($Quiet) { & $Path @Arguments | Out-Null } else { & $Path @Arguments }
    return $LASTEXITCODE
}

if ($Uninstall -or (Get-EnvValue 'HYPHA_UNINSTALL') -eq '1') {
    if (Test-Path $Exe) {
        Invoke-Agent $Exe @('stop') -Quiet | Out-Null
        if ((Invoke-Agent $Exe @('uninstall') -Quiet) -ne 0) { Stop-Install 'suppression du service impossible' }
    }
    if (Test-Path $BinDir) { Remove-Item -Recurse -Force $BinDir }
    Write-Info "conf et données gardées dans $DataDir (à supprimer à la main pour tout effacer)"
    Write-Output 'hypha-agent désinstallé'
    return
}

$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -ne 'AMD64' -and $arch -ne 'ARM64') { Stop-Install "architecture non prise en charge : $arch" }

# --- Téléchargement, vérifié par SHA256SUMS ----------------------------------------------
$name = 'hypha-agent-windows-amd64.exe'
$tmp = Join-Path ([IO.Path]::GetTempPath()) ('hypha-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $file = Join-Path $tmp $name
    $sumFile = Join-Path $tmp 'SHA256SUMS'
    try {
        Get-File "$Url/install/$name" $file
        Get-File "$Url/install/SHA256SUMS" $sumFile
    } catch {
        Stop-Install "téléchargement impossible depuis $Url/install/ : $($_.Exception.Message)"
    }
    $expected = $null
    foreach ($line in Get-Content $sumFile) {
        $parts = $line.Trim() -split '\s+'
        if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $name) { $expected = $parts[0].ToLower(); break }
    }
    if (-not $expected) { Stop-Install "$name absent de SHA256SUMS" }
    $actual = (Get-FileHash -Algorithm SHA256 -Path $file).Hash.ToLower()
    if ($expected -ne $actual) { Stop-Install "somme SHA-256 incorrecte pour $name (attendue $expected, obtenue $actual)" }
    $version = ((& $file version) -split ' ')[1]
    if (-not $version) { Stop-Install 'le binaire téléchargé ne s''exécute pas' }

    # --- Binaire : remplacé seulement s'il diffère ------------------------------------------
    $binChanged = $false
    if ((Test-Path $Exe) -and ((Get-FileHash -Algorithm SHA256 -Path $Exe).Hash.ToLower() -eq $actual)) {
        Write-Info "binaire $version déjà en place"
    } else {
        New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
        $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
        if ($svc -and $svc.Status -ne 'Stopped' -and (Test-Path $Exe)) {
            # Un exécutable en cours d'exécution ne peut pas être remplacé.
            if ((Invoke-Agent $Exe @('stop') -Quiet) -ne 0) { Stop-Install 'arrêt du service impossible avant mise à jour' }
        }
        Copy-Item -Force -Path $file -Destination $Exe
        $binChanged = $true
        Write-Info "binaire $version posé : $Exe"
    }

    # --- Configuration --------------------------------------------------------------------
    function ConvertTo-YamlString([string]$s) { '"' + ($s -replace '\\', '\\' -replace '"', '\"') + '"' }

    function Get-TagsYaml {
        $raw = Get-EnvValue 'HYPHA_TAGS'
        if ([string]::IsNullOrWhiteSpace($raw)) { return '{}' }
        $raw = $raw.Trim()
        $pairs = [ordered]@{}
        if ($raw.StartsWith('{')) {
            try { $obj = $raw | ConvertFrom-Json } catch { Stop-Install "HYPHA_TAGS : JSON invalide : $raw" }
            foreach ($p in $obj.PSObject.Properties) { $pairs[$p.Name] = [string]$p.Value }
        } else {
            foreach ($item in $raw.Split(',')) {
                $item = $item.Trim()
                if (-not $item) { continue }
                $i = $item.IndexOf(':')
                if ($i -lt 0) { $pairs[$item] = '' } else { $pairs[$item.Substring(0, $i)] = $item.Substring($i + 1) }
            }
        }
        $out = @()
        foreach ($k in $pairs.Keys) {
            if ($k -cnotmatch '^[a-z0-9_./-]+$') { Stop-Install "HYPHA_TAGS : clé « $k » hors de [a-z0-9_.-/]" }
            $out += "${k}: $(ConvertTo-YamlString $pairs[$k])"
        }
        return '{' + ($out -join ', ') + '}'
    }

    # Vérifie une conf telle que le service la lira : sans HYPHA_API_KEY ni HYPHA_URL.
    function Test-Conf([string]$Binary, [string]$Path) {
        $saved = @{}
        foreach ($v in 'HYPHA_API_KEY', 'HYPHA_URL', 'HYPHA_TLS_PIN') {
            $saved[$v] = Get-EnvValue $v
            [Environment]::SetEnvironmentVariable($v, $null)
        }
        try { $rc = Invoke-Agent $Binary @('check-config', '--config', $Path) -Quiet }
        finally { foreach ($v in $saved.Keys) { [Environment]::SetEnvironmentVariable($v, $saved[$v]) } }
        if ($rc -ne 0) { Stop-Install "configuration refusée par check-config (détail ci-dessus) : $Path" }
    }

    # Répertoire de données réservé à SYSTEM et aux administrateurs (SID, valables quelle que
    # soit la langue de Windows) : il contient la clé, l'identité et le tampon des envois.
    New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
    & icacls $DataDir /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
    if ($LASTEXITCODE -ne 0) { Stop-Install "droits de $DataDir non posés" }
    # Compléments de conf (conf.d\*.yaml, fusionnés après hypha.yaml), droits hérités.
    New-Item -ItemType Directory -Force -Path (Join-Path $DataDir 'conf.d') | Out-Null

    $confChanged = $false
    $newConf = Join-Path $tmp 'hypha.yaml'
    if (-not (Test-Path $Conf)) {
        $key = Get-EnvValue 'HYPHA_API_KEY'
        if ([string]::IsNullOrWhiteSpace($key)) { Stop-Install 'HYPHA_API_KEY manquante (première installation)' }
        $hostname = Get-EnvValue 'HYPHA_HOSTNAME'
        if ($null -eq $hostname) { $hostname = '' }
        $buffer = (Join-Path $DataDir 'buffer') -replace '\\', '/'
        $text = @"
# Configuration de hypha-agent, écrite par install.ps1. Vérifier : hypha-agent check-config
api_key: $(ConvertTo-YamlString $key.Trim())
url: $(ConvertTo-YamlString $Url)
$(if ($Pin) { "tls_pin: $(ConvertTo-YamlString $Pin)" })
hostname: $(ConvertTo-YamlString $hostname)
tags: $(Get-TagsYaml)
interval: 15s
buffer: {dir: "$buffer", max_mb: 256}
logs:
  enabled: true
  windows_eventlog: [System, Application]
  files: []
  exclude_patterns: []
processes: {enabled: true, include: []}
network: {enabled: true}
checks:
  windows_services: []
  windows_services_exclude: []
  windows_auto_services: auto
  http: []
  tcp: []
statsd: {enabled: true, listen: "127.0.0.1:8125"}
otlp: {enabled: false, listen: "127.0.0.1:4318"}
"@
        [IO.File]::WriteAllText($newConf, ($text -replace "`r`n", "`n"), $Utf8NoBom)
        Test-Conf $file $newConf
        Copy-Item -Force -Path $newConf -Destination $Conf
        $confChanged = $true
        Write-Info "configuration créée : $Conf"
    } elseif ((Get-EnvValue 'HYPHA_FORCE_CONF') -eq '1') {
        $text = [IO.File]::ReadAllText($Conf)
        $orig = $text
        $key = Get-EnvValue 'HYPHA_API_KEY'
        $sets = @{}
        if (-not [string]::IsNullOrWhiteSpace($key)) { $sets['api_key'] = $key.Trim() }
        if ($UrlGiven) { $sets['url'] = $Url }
        if ($Pin) { $sets['tls_pin'] = $Pin }
        foreach ($k in $sets.Keys) {
            $line = "${k}: $(ConvertTo-YamlString $sets[$k])"
            $re = "(?m)^$k\s*:.*$"
            if ($text -match $re) { $text = [regex]::Replace($text, $re, $line.Replace('$', '$$')) } else { $text = $text.TrimEnd() + "`n$line`n" }
        }
        if ($text -ceq $orig) {
            Write-Info "configuration inchangée : $Conf"
        } else {
            [IO.File]::WriteAllText($newConf, $text, $Utf8NoBom)
            Test-Conf $file $newConf
            Copy-Item -Force -Path $newConf -Destination $Conf
            $confChanged = $true
            Write-Info "api_key et url réécrites (HYPHA_FORCE_CONF=1) : $Conf"
        }
    } elseif ($Pin -and -not ([IO.File]::ReadAllText($Conf) -match '(?m)^tls_pin\s*:')) {
        # Sans l'empreinte, l'agent refuserait le certificat que l'installation vient d'accepter.
        $text = [IO.File]::ReadAllText($Conf).TrimEnd() + "`ntls_pin: $(ConvertTo-YamlString $Pin)`n"
        [IO.File]::WriteAllText($newConf, $text, $Utf8NoBom)
        Test-Conf $file $newConf
        Copy-Item -Force -Path $newConf -Destination $Conf
        $confChanged = $true
        Write-Info "configuration existante gardée, tls_pin ajouté : $Conf"
    } else {
        Write-Info "configuration existante gardée telle quelle : $Conf"
    }
    & icacls $Conf /inheritance:r /grant:r '*S-1-5-18:F' '*S-1-5-32-544:F' | Out-Null
    if ($LASTEXITCODE -ne 0) { Stop-Install "droits de $Conf non posés" }
    Test-Conf $Exe $Conf

    # --- Service ----------------------------------------------------------------------------
    if (-not (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue)) {
        if ((Invoke-Agent $Exe @('install', '--config', $Conf) -Quiet) -ne 0) { Stop-Install 'création du service impossible' }
        Write-Info "service $ServiceName créé (démarrage automatique, relance sur échec)"
    }
    $svc = Get-Service -Name $ServiceName
    if ($svc.Status -ne 'Running') {
        if ((Invoke-Agent $Exe @('start') -Quiet) -ne 0) { Stop-Install "démarrage refusé (voir $DataDir\agent.log)" }
        Write-Info 'service démarré'
    } elseif ($binChanged -or $confChanged) {
        Invoke-Agent $Exe @('stop') -Quiet | Out-Null
        if ((Invoke-Agent $Exe @('start') -Quiet) -ne 0) { Stop-Install "redémarrage refusé (voir $DataDir\agent.log)" }
        Write-Info 'service redémarré'
    } else {
        Write-Info 'service déjà actif, rien à redémarrer'
    }

    # Un agent qui s'arrête juste après le démarrage (clé refusée, conf) doit se voir ici.
    Start-Sleep -Seconds 3
    $svc = Get-Service -Name $ServiceName
    if ($svc.Status -ne 'Running') { Stop-Install "le service ne reste pas actif (voir $DataDir\agent.log)" }
    Write-Info "service $ServiceName : $($svc.Status), démarrage $($svc.StartType)"
    Write-Output "hypha-agent $version actif"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

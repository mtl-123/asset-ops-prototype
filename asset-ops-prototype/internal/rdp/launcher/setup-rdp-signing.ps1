[CmdletBinding()]
param(
    [ValidateSet("Status", "Install", "InstallPolicy")]
    [string]$Mode = "Status",
    [string]$PolicyEntry,
    [string]$ResultPath
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$subject = "CN=Pulse RDP Launcher"
$userPolicyPath = "HKCU:\SOFTWARE\Policies\Microsoft\Windows NT\Terminal Services"
$machinePolicyPath = "HKLM:\SOFTWARE\Policies\Microsoft\Windows NT\Terminal Services"
$admxPath = Join-Path $env:SystemRoot "PolicyDefinitions\TerminalServer.admx"

function Get-PulseCertificate {
    $store = [Security.Cryptography.X509Certificates.X509Store]::new("My", "CurrentUser")
    $store.Open([Security.Cryptography.X509Certificates.OpenFlags]::ReadOnly)
    try {
        return @($store.Certificates | Where-Object {
            $_.Subject -eq $subject -and $_.HasPrivateKey -and $_.NotAfter -gt (Get-Date).AddDays(30)
        } | Sort-Object NotAfter -Descending)[0]
    }
    finally { $store.Close() }
}

function Test-CertificateInStore {
    param([string]$StoreName, [string]$Thumbprint)
    $store = [Security.Cryptography.X509Certificates.X509Store]::new($StoreName, "CurrentUser")
    $store.Open([Security.Cryptography.X509Certificates.OpenFlags]::ReadOnly)
    try { return [bool]@($store.Certificates | Where-Object Thumbprint -eq $Thumbprint).Count }
    finally { $store.Close() }
}

function Add-PublicCertificateToStore {
    param([string]$StoreName, $Certificate)
    if (Test-CertificateInStore -StoreName $StoreName -Thumbprint $Certificate.Thumbprint) { return }
    $publicBytes = $Certificate.Export([Security.Cryptography.X509Certificates.X509ContentType]::Cert)
    $publicCertificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new($publicBytes)
    $store = [Security.Cryptography.X509Certificates.X509Store]::new($StoreName, "CurrentUser")
    $store.Open([Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite)
    try { $store.Add($publicCertificate) }
    finally { $store.Close(); $publicCertificate.Dispose() }
}

function Get-Sha256Thumbprint {
    param([Parameter(Mandatory = $true)]$Certificate)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha256.ComputeHash($Certificate.RawData))).Replace("-", "")
    }
    finally {
        $sha256.Dispose()
    }
}

function Get-PolicyEntries {
    param([string[]]$Paths = @($userPolicyPath, $machinePolicyPath))
    $entries = foreach ($path in $Paths) {
        try {
            $value = (Get-ItemProperty -Path $path -Name "TrustedCertThumbprints" -ErrorAction SilentlyContinue).TrustedCertThumbprints
            if (-not [string]::IsNullOrWhiteSpace([string]$value)) {
                [string]$value -split "," | ForEach-Object { $_.Trim() } | Where-Object { $_ }
            }
        }
        catch {
            # A locked-down user policy key must not hide a valid machine policy.
        }
    }
    return @($entries | Select-Object -Unique)
}

function Test-PolicyEntry {
    param([string]$Value)
    return [bool]($Value -match '^(sha256:[0-9A-Fa-f]{64}|[0-9A-Fa-f]{40})$')
}

function Write-ResultFile {
    param($Value)
    if ([string]::IsNullOrWhiteSpace($ResultPath)) { return }
    $json = $Value | ConvertTo-Json -Compress
    [IO.File]::WriteAllText($ResultPath, $json, [Text.UTF8Encoding]::new($false))
}

function Get-SetupStatus {
    $certificate = Get-PulseCertificate
    if (-not $certificate) {
        return @{ ok = $true; configured = $false; reason = "signing certificate is not installed" }
    }

    $sha256Thumbprint = Get-Sha256Thumbprint -Certificate $certificate
    $supportsSha2 = (Test-Path $admxPath) -and [bool](Select-String -LiteralPath $admxPath -Pattern "DisableSHA1CertThumbprints" -Quiet)
    $policyEntry = if ($supportsSha2) { "sha256:$sha256Thumbprint" } else { $certificate.Thumbprint }
    $policyConfigured = (Get-PolicyEntries) -contains $policyEntry
    $rootTrusted = Test-CertificateInStore -StoreName "Root" -Thumbprint $certificate.Thumbprint
    $publisherTrusted = Test-CertificateInStore -StoreName "TrustedPublisher" -Thumbprint $certificate.Thumbprint

    return @{
        ok = $true
        configured = $policyConfigured -and $rootTrusted -and $publisherTrusted
        publisher = "Pulse RDP Launcher"
        certificateThumbprint = $certificate.Thumbprint
        policyThumbprint = $policyEntry
        signingHash = $(if ($supportsSha2) { "sha256" } else { "sha1" })
        rootTrusted = $rootTrusted
        publisherTrusted = $publisherTrusted
        policyConfigured = $policyConfigured
        expires = $certificate.NotAfter.ToString("o")
    }
}

if ($Mode -eq "InstallPolicy") {
    $result = @{ ok = $false; policyConfigured = $false }
    try {
        if (-not (Test-PolicyEntry -Value $PolicyEntry)) { throw "Invalid trusted RDP publisher fingerprint" }
        $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
        $principal = [Security.Principal.WindowsPrincipal]::new($identity)
        if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
            throw "Administrator privileges are required to install the RDP publisher policy"
        }

        $entries = @(Get-PolicyEntries -Paths @($machinePolicyPath))
        if ($entries -notcontains $PolicyEntry) { $entries += $PolicyEntry }
        New-Item -Path $machinePolicyPath -Force | Out-Null
        Set-ItemProperty -Path $machinePolicyPath -Name "TrustedCertThumbprints" -Type String -Value ($entries -join ",")
        $result = @{ ok = $true; policyConfigured = $true }
        Write-ResultFile -Value $result
        [Console]::Out.WriteLine(($result | ConvertTo-Json -Compress))
        exit 0
    }
    catch {
        $result.error = $_.Exception.Message
        Write-ResultFile -Value $result
        [Console]::Error.WriteLine($_.Exception.Message)
        exit 1
    }
}

if ($Mode -eq "Install") {
    $certificate = Get-PulseCertificate
    if (-not $certificate) {
        Import-Module (Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\Modules\Microsoft.PowerShell.Security\Microsoft.PowerShell.Security.psd1") -Force
        if (-not (Get-PSDrive -Name Cert -ErrorAction SilentlyContinue)) {
            New-PSDrive -Name Cert -PSProvider Certificate -Root "\" -Scope Global | Out-Null
        }
        Import-Module (Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\Modules\PKI\pki.psd1") -Force
        $certificate = New-SelfSignedCertificate `
            -Type CodeSigningCert `
            -Subject $subject `
            -FriendlyName "Pulse trusted RDP file publisher" `
            -KeyAlgorithm RSA `
            -KeyLength 2048 `
            -HashAlgorithm SHA256 `
            -KeyExportPolicy NonExportable `
            -NotAfter (Get-Date).AddYears(3) `
            -CertStoreLocation Cert:\CurrentUser\My
    }

    Add-PublicCertificateToStore -StoreName "Root" -Certificate $certificate
    Add-PublicCertificateToStore -StoreName "TrustedPublisher" -Certificate $certificate

    $sha256Thumbprint = Get-Sha256Thumbprint -Certificate $certificate
    $supportsSha2 = (Test-Path $admxPath) -and [bool](Select-String -LiteralPath $admxPath -Pattern "DisableSHA1CertThumbprints" -Quiet)
    $policyEntry = if ($supportsSha2) { "sha256:$sha256Thumbprint" } else { $certificate.Thumbprint }
    if (-not (Test-PolicyEntry -Value $policyEntry)) { throw "Invalid trusted RDP publisher fingerprint" }

    if ((Get-PolicyEntries) -notcontains $policyEntry) {
        $resultFile = Join-Path ([IO.Path]::GetTempPath()) ("pulse-rdp-policy-{0}.json" -f [Guid]::NewGuid().ToString("N"))
        $powershell = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
        $escapedScript = '"' + $PSCommandPath.Replace('"', '\"') + '"'
        $escapedResult = '"' + $resultFile.Replace('"', '\"') + '"'
        try {
            $process = Start-Process -FilePath $powershell -Verb RunAs -WindowStyle Hidden -Wait -PassThru -ArgumentList @(
                "-NoProfile",
                "-NonInteractive",
                "-ExecutionPolicy", "Bypass",
                "-File", $escapedScript,
                "-Mode", "InstallPolicy",
                "-PolicyEntry", $policyEntry,
                "-ResultPath", $escapedResult
            )
            if ($process.ExitCode -ne 0 -or -not (Test-Path -LiteralPath $resultFile)) {
                throw "The trusted RDP publisher policy was not installed. Approve the Windows administrator prompt and try again."
            }
            $policyResult = Get-Content -LiteralPath $resultFile -Raw | ConvertFrom-Json
            if (-not $policyResult.ok) { throw ([string]$policyResult.error) }
        }
        finally {
            Remove-Item -LiteralPath $resultFile -Force -ErrorAction SilentlyContinue
        }
    }
}

[Console]::Out.WriteLine(((Get-SetupStatus) | ConvertTo-Json -Compress))

[CmdletBinding()]
param(
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$publisherSubject = "CN=Pulse RDP Launcher"
$policyPaths = @(
    "HKCU:\SOFTWARE\Policies\Microsoft\Windows NT\Terminal Services",
    "HKLM:\SOFTWARE\Policies\Microsoft\Windows NT\Terminal Services"
)
$admxPath = Join-Path $env:SystemRoot "PolicyDefinitions\TerminalServer.admx"

Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;

public static class PulseCredentialManager
{
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    public struct Credential
    {
        public UInt32 Flags;
        public UInt32 Type;
        public string TargetName;
        public string Comment;
        public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
        public UInt32 CredentialBlobSize;
        public IntPtr CredentialBlob;
        public UInt32 Persist;
        public UInt32 AttributeCount;
        public IntPtr Attributes;
        public string TargetAlias;
        public string UserName;
    }

    [DllImport("advapi32.dll", EntryPoint = "CredWriteW", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern bool CredWrite(ref Credential credential, UInt32 flags);

    [DllImport("advapi32.dll", EntryPoint = "CredDeleteW", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern bool CredDelete(string target, UInt32 type, UInt32 flags);
}
"@

if ($SelfTest) {
    [Console]::Out.WriteLine((@{ ok = $true; credentialApi = $true; mstsc = (Test-Path (Join-Path $env:SystemRoot "System32\mstsc.exe")); rdpsign = (Test-Path (Join-Path $env:SystemRoot "System32\rdpsign.exe")) } | ConvertTo-Json -Compress))
    exit 0
}

function Get-Sha256Thumbprint {
    param([Parameter(Mandatory = $true)]$Certificate)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($sha256.ComputeHash($Certificate.RawData))).Replace("-", "") }
    finally { $sha256.Dispose() }
}

function Get-SigningConfiguration {
    $store = [Security.Cryptography.X509Certificates.X509Store]::new("My", "CurrentUser")
    $store.Open([Security.Cryptography.X509Certificates.OpenFlags]::ReadOnly)
    try {
        $certificate = @($store.Certificates | Where-Object {
            $_.Subject -eq $publisherSubject -and $_.HasPrivateKey -and $_.NotAfter -gt (Get-Date)
        } | Sort-Object NotAfter -Descending)[0]
    }
    finally { $store.Close() }
    if (-not $certificate) { return $null }

    $entries = foreach ($policyPath in $policyPaths) {
        try {
            $policyValue = (Get-ItemProperty -Path $policyPath -Name "TrustedCertThumbprints" -ErrorAction SilentlyContinue).TrustedCertThumbprints
            [string]$policyValue -split "," | ForEach-Object { $_.Trim() } | Where-Object { $_ }
        }
        catch {
            # Continue to the other policy scope when one scope is inaccessible.
        }
    }
    $entries = @($entries | Select-Object -Unique)
    $supportsSha2 = (Test-Path $admxPath) -and [bool](Select-String -LiteralPath $admxPath -Pattern "DisableSHA1CertThumbprints" -Quiet)
    $sha256Thumbprint = Get-Sha256Thumbprint -Certificate $certificate
    $policyEntry = if ($supportsSha2) { "sha256:$sha256Thumbprint" } else { $certificate.Thumbprint }
    if ($entries -notcontains $policyEntry) { return $null }

    return @{
        Certificate = $certificate
        Algorithm = $(if ($supportsSha2) { "sha256" } else { "sha1" })
        # rdpsign uses the SHA-1 certificate thumbprint to locate the key even
        # when /sha256 selects the signature algorithm on current Windows builds.
        SigningThumbprint = $certificate.Thumbprint
    }
}

function Write-TemporaryCredential {
    param(
        [Parameter(Mandatory = $true)][string]$Target,
        [Parameter(Mandatory = $true)][string]$Username,
        [Parameter(Mandatory = $true)][string]$Password
    )

    $passwordPointer = [Runtime.InteropServices.Marshal]::StringToCoTaskMemUni($Password)
    try {
        $credential = New-Object PulseCredentialManager+Credential
        $credential.Flags = 0
        $credential.Type = 1
        $credential.TargetName = $Target
        $credential.CredentialBlobSize = [Text.Encoding]::Unicode.GetByteCount($Password)
        $credential.CredentialBlob = $passwordPointer
        $credential.Persist = 1
        $credential.UserName = $Username
        if (-not [PulseCredentialManager]::CredWrite([ref]$credential, 0)) {
            throw [ComponentModel.Win32Exception]::new([Runtime.InteropServices.Marshal]::GetLastWin32Error())
        }
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeCoTaskMemUnicode($passwordPointer)
    }
}

$profile = ([Console]::In.ReadToEnd() | ConvertFrom-Json)
$address = [string]$profile.address
$port = [int]$profile.port
$username = [string]$profile.username
$password = [string]$profile.password

if ([string]::IsNullOrWhiteSpace($address) -or $address -match "[\s\r\n]") { throw "Invalid target address" }
if ($port -lt 1 -or $port -gt 65535) { throw "Invalid RDP port" }
if ($username -match "[\r\n]") { throw "Invalid username" }

$displayAddress = if ($address.Contains(":") -and -not $address.StartsWith("[")) { "[$address]" } else { $address }
$fullAddress = "${displayAddress}:$port"
$credentialTarget = "TERMSRV/$address"
$rdpPath = Join-Path ([IO.Path]::GetTempPath()) ("pulse-rdp-{0}.rdp" -f [Guid]::NewGuid().ToString("N"))
$hasCredential = -not [string]::IsNullOrEmpty($username) -and -not [string]::IsNullOrEmpty($password)
$signing = Get-SigningConfiguration

if (-not $signing) {
    [Console]::Out.WriteLine((@{ ok = $false; code = "signing_setup_required"; error = "One-click RDP signing setup is required" } | ConvertTo-Json -Compress))
    [Console]::Out.Flush()
    exit 2
}

$driveSetting = ""
if ([bool]$profile.redirectDrives) {
    if ([string]$profile.driveMode -eq "custom") {
        $drives = @([string]$profile.driveList -split "[;,]+" | ForEach-Object { $_.Trim() } | Where-Object { $_ })
        if ($drives.Count -eq 0 -or @($drives | Where-Object { $_ -notmatch "^[A-Za-z]:\\?$" }).Count -gt 0) {
            throw "Invalid drive list"
        }
        $driveSetting = (($drives | ForEach-Object { $_.Substring(0, 1).ToUpperInvariant() + ":\" }) -join ";")
    }
    else {
        $driveSetting = "*"
    }
}

$rdpLines = @(
    "full address:s:$fullAddress"
    "username:s:$username"
    ("prompt for credentials:i:{0}" -f $(if ($hasCredential) { 0 } else { 1 }))
    "authentication level:i:2"
    "enablecredsspsupport:i:1"
    "negotiate security layer:i:1"
    ("redirectclipboard:i:{0}" -f $(if ([bool]$profile.redirectClipboard) { 1 } else { 0 }))
    ("redirectdrives:i:{0}" -f $(if ([bool]$profile.redirectDrives) { 1 } else { 0 }))
    "drivestoredirect:s:$driveSetting"
    ("screen mode id:i:{0}" -f $(if ([bool]$profile.fullscreen) { 2 } else { 1 }))
    ("use multimon:i:{0}" -f $(if ([bool]$profile.multiMonitor) { 1 } else { 0 }))
    "smart sizing:i:1"
    "dynamic resolution:i:1"
    "networkautodetect:i:1"
    "bandwidthautodetect:i:1"
    "connection type:i:7"
    "compression:i:1"
    "bitmapcachepersistenable:i:1"
)

try {
    if ($hasCredential) { Write-TemporaryCredential -Target $credentialTarget -Username $username -Password $password }
    [IO.File]::WriteAllLines($rdpPath, $rdpLines, [Text.Encoding]::Unicode)
    $rdpsign = Join-Path $env:SystemRoot "System32\rdpsign.exe"
    $signOutput = & $rdpsign ("/{0}" -f $signing.Algorithm) $signing.SigningThumbprint /q $rdpPath 2>&1
    if ($LASTEXITCODE -ne 0) { throw "rdpsign failed: $signOutput" }
    $process = Start-Process -FilePath (Join-Path $env:SystemRoot "System32\mstsc.exe") -ArgumentList ('"{0}"' -f $rdpPath) -PassThru
    [Console]::Out.WriteLine((@{ ok = $true; pid = $process.Id; target = $fullAddress } | ConvertTo-Json -Compress))
    [Console]::Out.Flush()
    Start-Sleep -Seconds 30
}
finally {
    if ($hasCredential) { [void][PulseCredentialManager]::CredDelete($credentialTarget, 1, 0) }
    Remove-Item -LiteralPath $rdpPath -Force -ErrorAction SilentlyContinue
}

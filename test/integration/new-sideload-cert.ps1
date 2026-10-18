# Creates a throwaway self-signed code-signing certificate for CN=SofCatIT, the
# Publisher in the msix fixtures' AppxManifest.xml, and trusts it machine-wide
# so Add-AppxProvisionedPackage accepts fixtures signed with it. It adds a
# certificate to LocalMachine\Root: run it only on disposable CI runners and
# test VMs, as an administrator.
#
# Prints the thumbprint, for prepare-release-integration.ps1 -MsixCertThumbprint.

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$cert = New-SelfSignedCertificate `
    -Subject "CN=SofCatIT" `
    -CertStoreLocation "Cert:\CurrentUser\My" `
    -Type CodeSigningCert `
    -TextExtension @("2.5.29.37={text}1.3.6.1.5.5.7.3.3")
$certPath = Join-Path ([System.IO.Path]::GetTempPath()) "sofcat-it-sideload-$($cert.Thumbprint).cer"
Export-Certificate -Cert $cert -FilePath $certPath | Out-Null
foreach ($store in "TrustedPeople", "Root") {
    Import-Certificate -FilePath $certPath -CertStoreLocation "Cert:\LocalMachine\$store" | Out-Null
}
Remove-Item -LiteralPath $certPath
Write-Host "[INFO] Sideload certificate installed: $($cert.Thumbprint)"
$cert.Thumbprint

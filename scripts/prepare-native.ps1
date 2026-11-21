param([string]$Toolchain='D:\msys64\ucrt64',[switch]$SkipOpenSSLBuild)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$json=Join-Path $root '.tools\native-json\nlohmann\json.hpp'
New-Item -ItemType Directory -Path (Split-Path -Parent $json) -Force|Out-Null
if(-not(Test-Path -LiteralPath $json)){Invoke-WebRequest 'https://raw.githubusercontent.com/nlohmann/json/v3.12.0/single_include/nlohmann/json.hpp' -OutFile $json}
if((Get-FileHash -LiteralPath $json -Algorithm SHA256).Hash.ToLower() -ne 'aaf127c04cb31c406e5b04a63f1ae89369fccde6d8fa7cdda1ed4f32dfc5de63'){throw 'JSON source checksum mismatch'}
$wintun=Join-Path $root '.tools\wintun-0.14.1.zip'
if(-not(Test-Path -LiteralPath $wintun)){Invoke-WebRequest 'https://www.wintun.net/builds/wintun-0.14.1.zip' -OutFile $wintun}
if((Get-FileHash -LiteralPath $wintun -Algorithm SHA256).Hash.ToLower() -ne '07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51'){throw 'Wintun archive checksum mismatch'}
$driver=Join-Path $root '.tools\wintun-package\wintun\bin\amd64\wintun.dll'
if(-not(Test-Path -LiteralPath $driver)){Expand-Archive -LiteralPath $wintun -DestinationPath (Join-Path $root '.tools\wintun-package')}
if((Get-AuthenticodeSignature -LiteralPath $driver).Status -ne 'Valid'){throw 'Wintun signature invalid'}
$sslSource=Join-Path $root '.tools\openssl-source'
$sslRoot=Join-Path $root '.tools\openssl-native'
$commit='2f4e3a5fec05394068cfa7379890ab552483a8a6'
if(-not(Test-Path -LiteralPath $sslSource)){git clone --depth 1 --branch openssl-4.1.0 https://github.com/openssl/openssl.git $sslSource;if($LASTEXITCODE -ne 0){throw 'OpenSSL clone failed'}}
if((git -C $sslSource rev-parse HEAD) -ne $commit){throw 'OpenSSL source revision mismatch'}
if(-not(Test-Path -LiteralPath (Join-Path $sslRoot 'lib64\libssl.a'))){
 if($SkipOpenSSLBuild){throw 'OpenSSL 4.1.0 static libraries unavailable'}
 function Posix([string]$p){$p=[IO.Path]::GetFullPath($p).Replace('\','/');return '/'+$p.Substring(0,1).ToLower()+$p.Substring(2)}
 $bash=Join-Path (Split-Path -Parent $Toolchain) 'usr\bin\bash.exe'
 if(-not(Test-Path -LiteralPath $bash)){throw 'MSYS2 bash/perl/make and UCRT64 GCC are required'}
 $command='export PATH="$2/bin:$2/../usr/bin:$PATH"; cd "$1"; "$2/../usr/bin/perl.exe" Configure mingw64 no-shared no-tests no-module no-asm no-legacy --prefix="$3" && "$2/../usr/bin/make.exe" -j8 && "$2/../usr/bin/make.exe" install_sw'
 & $bash '--noprofile' '--norc' '-c' $command '--' (Posix $sslSource) (Posix $Toolchain) $sslRoot.Replace('\','/')
 if($LASTEXITCODE -ne 0){throw 'OpenSSL static build failed'}
}
Write-Host 'Native inputs verified: OpenSSL 4.1.0, JSON 3.12.0, signed Wintun 0.14.1.'

function Test-NetGoServiceImage([string]$CommandLine,[string]$Expected) {
 $image=''
 if($CommandLine -match '^"([^"\r\n]+)"(?:\s|$)'){$image=$matches[1]}
 elseif($CommandLine -match '^([^\s"\r\n]+)(?:\s|$)'){$image=$matches[1]}
 if(-not $image){return $false}
 try{return [String]::Equals([IO.Path]::GetFullPath($image),[IO.Path]::GetFullPath($Expected),[StringComparison]::OrdinalIgnoreCase)}catch{return $false}
}

function Get-NetGoServiceEnvironment([string]$StateDirectory,[string[]]$Previous=@()) {
 $values=New-Object 'System.Collections.Generic.List[string]'
 foreach($entry in $Previous){
  if($entry -and $entry -notlike 'NB_STATE_DIR=*' -and $entry -notlike 'NB_LAZY_CONN=*'){$values.Add($entry)}
 }
 $values.Add('NB_STATE_DIR='+$StateDirectory)
 $values.Add('NB_LAZY_CONN=false')
 return $values.ToArray()
}

# This key belongs to this product only; preserve its typed values for rollback.
function Get-NetGoApplicationRegistration {
 $path='HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\NetGoNative'
 if(-not(Test-Path -LiteralPath $path)){return $null}
 $key=Get-Item -LiteralPath $path
 $values=@($key.GetValueNames()|ForEach-Object {[pscustomobject]@{Name=$_;Kind=[string]$key.GetValueKind($_);Value=$key.GetValue($_)}})
 return [pscustomobject]@{Values=$values}
}
function Restore-NetGoApplicationRegistration($Snapshot) {
 $path='HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\NetGoNative'
 if(Test-Path -LiteralPath $path){Remove-Item -LiteralPath $path}
 if($null -ne $Snapshot){
  $key=New-Item -Path $path -Force
  foreach($entry in $Snapshot.Values){$key.SetValue($entry.Name,$entry.Value,[Microsoft.Win32.RegistryValueKind]::$($entry.Kind))}
 }
}
function Get-NetGoPackageVersion([string]$Executable) {
 $version=[Diagnostics.FileVersionInfo]::GetVersionInfo($Executable).ProductVersion
 if($version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?$'){throw 'Invalid NetGo package version'}
 return $version
}
function Register-NetGoApplication([string]$Directory,[string]$OwnerSID,[bool]$NetworkOnly,[string]$Version) {
 if($Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?$'){throw 'Invalid NetGo registration version'}
 $path='HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\NetGoNative'
 $key=New-Item -Path $path -Force
 $values=@{DisplayName=$(if($NetworkOnly){'NetGo Network Service'}else{'NetGo'});DisplayVersion=$Version;Publisher='NetGo Project';InstallLocation=$Directory;DisplayIcon=(Join-Path $Directory 'Uninstall.exe')+',0';UninstallString='"'+(Join-Path $Directory 'Uninstall.exe')+'" --uninstall';NetGoOwnerSID=$OwnerSID;NetworkOnly=[string]$NetworkOnly}
 foreach($name in $values.Keys){$key.SetValue($name,[string]$values[$name],[Microsoft.Win32.RegistryValueKind]::String)}
 foreach($name in @('NoModify','NoRepair')){$key.SetValue($name,1,[Microsoft.Win32.RegistryValueKind]::DWord)}
}

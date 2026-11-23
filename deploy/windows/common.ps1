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

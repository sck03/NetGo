function Test-NetGoServiceImage([string]$CommandLine,[string]$Expected) {
 $image=''
 if($CommandLine -match '^"([^"\r\n]+)"(?:\s|$)'){$image=$matches[1]}
 elseif($CommandLine -match '^([^\s"\r\n]+)(?:\s|$)'){$image=$matches[1]}
 if(-not $image){return $false}
 try{return [String]::Equals([IO.Path]::GetFullPath($image),[IO.Path]::GetFullPath($Expected),[StringComparison]::OrdinalIgnoreCase)}catch{return $false}
}

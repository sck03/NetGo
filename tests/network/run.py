#!/usr/bin/env python3
"""Collect real acceptance evidence. Never infer PASS from simulated connectivity."""
import argparse, datetime, hashlib, json, pathlib, subprocess, sys
p=argparse.ArgumentParser()
p.add_argument('--case',required=True,choices=[f'A{i:02d}' for i in range(1,34)])
p.add_argument('--environment',required=True,help='Reviewed environment description JSON')
p.add_argument('--output',required=True)
p.add_argument('command',nargs=argparse.REMAINDER)
a=p.parse_args()
if not a.command: p.error('supply an actual verification command after --')
command=a.command[1:] if a.command[0]=='--' else a.command
out=pathlib.Path(a.output);out.mkdir(parents=True,exist_ok=False)
env=json.loads(pathlib.Path(a.environment).read_text())
start=datetime.datetime.now(datetime.timezone.utc)
result=subprocess.run(command,capture_output=True,text=True,timeout=300)
# Evidence output must be manually reviewed for secrets before publishing.
(out/'verification.log').write_text(result.stdout+'\n'+result.stderr,encoding='utf-8')
record={'case':a.case,'start':start.isoformat(),'environment':env,'exit_code':result.returncode,'status':'REVIEW-REQUIRED','verification_sha256':hashlib.sha256((result.stdout+result.stderr).encode()).hexdigest(),'note':'Exit code alone does not establish path, identity, or access-control acceptance.'}
(out/'result.json').write_text(json.dumps(record,indent=2,ensure_ascii=False)+'\n',encoding='utf-8')
print(json.dumps({'case':a.case,'exit_code':result.returncode,'status':'REVIEW-REQUIRED'}))
sys.exit(result.returncode)

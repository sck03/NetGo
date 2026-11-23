#!/usr/bin/env python3
"""Verify NetGo table ownership, including nftables 1.0.6 JSON compatibility."""
import json
import subprocess
import sys

OWNERS={'netgo':'NetGo lease gate v1','netgo_relay':'NetGo relay redirect v1'}


def legacy_owner(text, name, expected):
    lines=text.strip().splitlines()
    return (len(lines)>=3 and lines[0].strip()=='table inet '+name+' {'
            and lines[1].strip()=='comment '+json.dumps(expected))


def verify(name):
    if name not in OWNERS:
        raise ValueError('unknown managed table')
    result=subprocess.run(['nft','-j','list','table','inet',name],check=True,capture_output=True,text=True,timeout=10)
    tables=[item['table'] for item in json.loads(result.stdout)['nftables'] if 'table' in item]
    if len(tables)!=1 or tables[0].get('family')!='inet' or tables[0].get('name')!=name:
        raise ValueError('unexpected nft table')
    table=tables[0]
    if table.get('comment')==OWNERS[name]:return
    if 'comment' not in table:
        text=subprocess.run(['nft','list','table','inet',name],check=True,capture_output=True,text=True,timeout=10).stdout
        if legacy_owner(text,name,OWNERS[name]):return
    raise ValueError('refusing to remove an unowned nft table')


if __name__=='__main__':
    try:
        if len(sys.argv)!=2:raise ValueError('expected netgo or netgo_relay')
        verify(sys.argv[1])
    except (ValueError,OSError,subprocess.SubprocessError) as error:
        raise SystemExit(str(error))

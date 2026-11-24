"""Resolve workflow inputs before expensive builds; never publish from push."""
import argparse
import json
import os
from pathlib import Path
import re
from build_config import config, source_commit

def boolean(value):
    if value in ('true', '1', True): return True
    if value in ('false', '0', '', False, None): return False
    raise ValueError('发布开关必须为 true/false')

def resolve(target, env=None):
    env = os.environ if env is None else env
    data = config(target, env.get('NETGO_REQUESTED_VERSION'), env.get('NETGO_REQUESTED_ARCH'))
    publish = boolean(env.get('NETGO_PUBLISH', 'false'))
    if publish and env.get('GITHUB_EVENT_NAME') != 'workflow_dispatch':
        raise ValueError('只有手动工作流明确选择发布时才能写入 Releases')
    channel = env.get('NETGO_RELEASE_TYPE') or 'prerelease'
    if channel not in ('draft', 'prerelease', 'stable'): raise ValueError('未知 Release 类型')
    if publish and channel == 'stable' and '-' in data['version'].split('+', 1)[0]:
        raise ValueError('正式 Release 请使用不含预发布后缀的版本号，例如 0.3.1')
    sdk = (env.get('NETGO_IOS_SDK') or 'both') if target == 'ios' else 'none'
    if sdk not in ('none', 'both', 'iphoneos', 'iphonesimulator'): raise ValueError('无效 iOS SDK')
    code = int(env.get('NETGO_VERSION_CODE') or '1')
    number = int(env.get('NETGO_BUILD_NUMBER') or '1')
    if not 1 <= code <= 2100000000 or not 1 <= number <= 2147483647: raise ValueError('构建号需为合法的正整数')
    run_id = env.get('GITHUB_RUN_ID') or 'local'
    if not re.fullmatch(r'[0-9]+|local', run_id): raise ValueError('无效运行 ID')
    artifact = f"{target}-{data['version']}-{data['arch']}-{sdk}-{run_id}"
    data.update(publish=str(publish).lower(), release_type=channel, sdk=sdk,
                version_code=str(code), build_number=str(number), artifact_name=artifact,
                release_artifact='release-' + artifact)
    return data

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', required=True)
    args = parser.parse_args()
    try: data = resolve(args.target)
    except (ValueError, TypeError) as error: parser.error(str(error))
    if os.environ.get('GITHUB_OUTPUT'):
        with Path(os.environ['GITHUB_OUTPUT']).open('a', encoding='utf-8') as output:
            for key, value in data.items(): output.write(f'{key}={value}\n')
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        with Path(os.environ['GITHUB_STEP_SUMMARY']).open('a', encoding='utf-8') as output:
            output.write('### 构建设置\n\n| 项目 | 值 |\n| --- | --- |\n')
            for key in ('target','version','arch','sdk','version_code','build_number','publish','release_type'):
                output.write(f'| {key} | {data[key]} |\n')
    print(json.dumps(data, ensure_ascii=False))

if __name__ == '__main__': main()

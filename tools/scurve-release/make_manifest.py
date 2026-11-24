"""Create the GitHub Release update manifest from the exact uploaded binaries."""
import argparse
import hashlib
import json
from pathlib import Path
import re


def make_manifest(version, repository, windows, macos, notes=''):
    version = version.removeprefix('v')
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', version):
        raise ValueError('Version must be X.Y.Z (optional v prefix)')
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository):
        raise ValueError('Repository must be owner/name')
    result = {'version': version, 'notes': notes}
    for platform, asset in [('windows', windows), ('macos', macos)]:
        asset = Path(asset)
        expected = 'S-curve-4s-Windows.exe' if platform == 'windows' else 'S-curve-4s-macOS.zip'
        if asset.name != expected:
            raise ValueError(f'{platform} asset must be named {expected}')
        digest = hashlib.sha256()
        with asset.open('rb') as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b''):
                digest.update(chunk)
        result[platform] = {
            'url': f'https://github.com/{repository}/releases/download/v{version}/{expected}',
            'sha256': digest.hexdigest(),
        }
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('version')
    parser.add_argument('--repository', default='CanICallYouJim/s-curve-4s')
    parser.add_argument('--windows', type=Path, required=True)
    parser.add_argument('--macos', type=Path, required=True)
    parser.add_argument('--notes', default='')
    parser.add_argument('--output', type=Path, default=Path('update.json'))
    args = parser.parse_args()
    try:
        manifest = make_manifest(args.version, args.repository, args.windows, args.macos, args.notes)
    except (OSError, ValueError) as error:
        parser.error(str(error))
    args.output.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(args.output)


if __name__ == '__main__':
    main()

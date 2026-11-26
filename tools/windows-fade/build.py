"""Build a standalone Windows x64 EXE from a verified Gyan essentials ZIP."""
import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
from zipfile import ZIP_DEFLATED, ZipFile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('archive', type=Path)
parser.add_argument('output', type=Path)
parser.add_argument('--check', action='store_true', help='Build the read-only music checker')
parser.add_argument('--version', default='1.2.3')
args = parser.parse_args()
root = Path(__file__).resolve().parent
archive = args.archive.resolve()
output = args.output.resolve()
expected = Path(str(archive) + '.sha256').read_text().split()[0].lower()
actual = hashlib.sha256(archive.read_bytes()).hexdigest()
if expected != actual:
    raise SystemExit('FFmpeg checksum mismatch')
output.parent.mkdir(parents=True, exist_ok=True)
with ZipFile(archive) as source, ZipFile(root / 'payload.zip', 'w', compression=ZIP_DEFLATED, compresslevel=9) as payload:
    for name in ('ffmpeg.exe', 'ffprobe.exe'):
        matches = [p for p in source.namelist() if p.endswith('/bin/' + name)]
        if len(matches) != 1:
            raise SystemExit(f'Expected exactly one {name}')
        payload.writestr(name, source.read(matches[0]))
    license_name = next(name for name in source.namelist() if name.endswith('/LICENSE'))
    (output.parent / 'FFmpeg-LICENSE.txt').write_bytes(source.read(license_name))
    readme_name = next(name for name in source.namelist() if name.endswith('/README.txt'))
    (output.parent / 'FFmpeg-README.txt').write_bytes(source.read(readme_name))
env = dict(os.environ, GOOS='windows', GOARCH='amd64', CGO_ENABLED='0', GOPROXY='off')
link_flags = '-s -w -X main.appVersion=' + args.version + (' -X main.applicationMode=check' if args.check else '')
subprocess.run(['go', 'build', '-trimpath', '-ldflags=' + link_flags, '-o', str(output), '.'], cwd=root, env=env, check=True)
shutil.copyfile(root / ('HEALTHCHECK.md' if args.check else 'README.md'), output.parent / 'Инструкция.txt')
(output.parent / (output.name + '.sha256')).write_text(hashlib.sha256(output.read_bytes()).hexdigest() + '  ' + output.name + '\n')
print(f'{output} ({output.stat().st_size / 1024**2:.1f} MiB)')

"""Build an offline universal macOS .app from verified static FFmpeg binaries."""
import argparse
import hashlib
import os
from pathlib import Path
import plistlib
import shutil
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('tools', type=Path, help='Verified amd64/ and arm64/ directories with ffmpeg and ffprobe')
parser.add_argument('output', type=Path, help='Destination .app')
parser.add_argument('--engine-source', type=Path, help='Optional isolated Go source tree')
parser.add_argument('--version', default='1.2.3', help='Application and updater version')
args = parser.parse_args()
root = Path(__file__).resolve().parent
engine = (args.engine_source or root.parent / 'windows-fade').resolve()
source_tools = args.tools.resolve()
app = args.output.resolve()
if app.suffix != '.app':
    raise SystemExit('Output must be an .app')
macos = app / 'Contents/MacOS'
resources = app / 'Contents/Resources'
work = app.parent / 'build-cache'
for path in [macos, resources / 'tools', work]:
    path.mkdir(parents=True, exist_ok=True)
env = dict(os.environ, GOCACHE='/tmp/lena-go-cache', GOMODCACHE='/tmp/lena-go-mod', GOPROXY='off', CGO_ENABLED='0')

def run(command, **kwargs):
    subprocess.run([str(v) for v in command], check=True, **kwargs)

def universal(inputs, output):
    run(['lipo', '-create', *inputs, '-output', output])
    output.chmod(0o755)
    run(['codesign', '--force', '--sign', '-', output])

ui = []
backends = []
for arch, goarch in [('x86_64', 'amd64'), ('arm64', 'arm64')]:
    native = work / ('SCurve-' + arch)
    run(['xcrun', 'swiftc', '-O', '-module-cache-path', '/tmp/lena-swift-cache', '-target', arch + '-apple-macos13.0', '-framework', 'AppKit', root / 'App.swift', '-o', native])
    ui.append(native)
    backend = work / ('fade-engine-' + arch)
    run(['go', 'build', '-trimpath', '-ldflags=-s -w -X main.appVersion=' + args.version, '-o', backend, '.'], cwd=engine, env=dict(env, GOOS='darwin', GOARCH=goarch))
    backends.append(backend)
universal(ui, macos / 'SCurve')
universal(backends, macos / 'fade-engine')
for name in ['ffmpeg', 'ffprobe']:
    inputs = []
    for arch in ['amd64', 'arm64']:
        binary = source_tools / arch / name
        archive = binary.with_suffix('.zip')
        checksum = binary.with_suffix('.zip.sha256')
        expected = checksum.read_text().split()[0].lower()
        if hashlib.sha256(archive.read_bytes()).hexdigest() != expected:
            raise SystemExit('FFmpeg archive checksum mismatch: ' + str(archive))
        # Match extracted binary against the verified ZIP, not just an adjacent file.
        from zipfile import ZipFile
        with ZipFile(archive) as z:
            if z.read(name) != binary.read_bytes():
                raise SystemExit('FFmpeg extracted binary mismatch: ' + str(binary))
        deps = subprocess.check_output(['otool', '-L', binary], text=True).splitlines()[1:]
        if any(not line.strip().startswith(('/usr/lib/', '/System/Library/')) for line in deps):
            raise SystemExit('FFmpeg must use system libraries only: ' + str(binary))
        inputs.append(binary)
    universal(inputs, resources / 'tools' / name)
shutil.copyfile(engine / 'brand.png', resources / 'brand.png')
shutil.copyfile(root / 'README.md', resources / 'Инструкция.txt')
shutil.copyfile(root / 'THIRD-PARTY.md', resources / 'THIRD-PARTY.md')
shutil.copyfile(root / 'FFmpeg-LICENSE.txt', resources / 'FFmpeg-LICENSE.txt')
shutil.copyfile(root / 'FFmpeg-build-info.txt', resources / 'FFmpeg-build-info.txt')
# Generate the icon from the same approved native application icon, with no artwork change.
iconset = work / 'AppIcon.iconset'
iconset.mkdir(exist_ok=True)
for size in [16, 32, 128, 256, 512]:
    for scale in [1, 2]:
        target = iconset / ('icon_' + str(size) + 'x' + str(size) + ('@2x' if scale == 2 else '') + '.png')
        run(['sips', '-z', str(size * scale), str(size * scale), engine / 'icon.png', '--out', target], stdout=subprocess.DEVNULL)
run(['iconutil', '-c', 'icns', iconset, '-o', resources / 'AppIcon.icns'])
info = dict(CFBundleName='S-кривая 4 сек', CFBundleDisplayName='S-кривая 4 сек', CFBundleExecutable='SCurve', CFBundleIdentifier='by.ig.scurve', CFBundleVersion=args.version, CFBundleShortVersionString=args.version, CFBundlePackageType='APPL', CFBundleIconFile='AppIcon', NSHighResolutionCapable=True, LSMinimumSystemVersion='13.0', NSPrincipalClass='NSApplication', NSDesktopFolderUsageDescription='Чтение выбранных треков и сохранение обработанных копий в папке результатов.', NSDocumentsFolderUsageDescription='Чтение выбранных треков и сохранение обработанных копий в папке результатов.', NSDownloadsFolderUsageDescription='Чтение выбранных треков и сохранение обработанных копий в папке результатов.', NSRemovableVolumesUsageDescription='Обработка выбранных треков на флешке и сохранение копий рядом.', NSNetworkVolumesUsageDescription='Обработка выбранных треков в сетевой папке и сохранение копий рядом.', CFBundleDocumentTypes=[dict(CFBundleTypeName='Folder', CFBundleTypeRole='Viewer', LSHandlerRank='Alternate', LSItemContentTypes=['public.folder'])])
with (app / 'Contents/Info.plist').open('wb') as f:
    plistlib.dump(info, f)
# Local ad-hoc signature supports Apple Silicon; distribution notarization needs a Developer ID.
run(['codesign', '--force', '--sign', '-', app])
run(['codesign', '--verify', '--deep', '--strict', app])
print(app)

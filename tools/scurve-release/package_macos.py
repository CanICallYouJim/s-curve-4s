"""Package only the .app, with UTF-8 paths and executable permissions."""
import argparse
from pathlib import Path
from zipfile import ZipFile, ZIP_DEFLATED


def package(app, output):
    app, output = Path(app), Path(output)
    if app.name != 'S-кривая 4 с.app' or not app.is_dir():
        raise ValueError('Expected S-кривая 4 с.app')
    output.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(output, 'w', ZIP_DEFLATED) as archive:
        for entry in sorted(app.rglob('*')):
            if entry.is_symlink():
                raise ValueError('Symlinks are not supported in update archives')
            if entry.is_file():
                archive.write(entry, entry.relative_to(app.parent).as_posix())
    with ZipFile(output) as archive:
        if archive.testzip() is not None:
            raise ValueError('Corrupt archive')
        executable = archive.getinfo(app.name + '/Contents/MacOS/SCurve')
        if not executable.flag_bits & 0x800 or not (executable.external_attr >> 16) & 0o111:
            raise ValueError('Missing UTF-8 filename flag or executable permissions')
    return output


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('app', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    print(package(args.app, args.output))

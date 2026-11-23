"""Extract reviewed archives without requiring a newer Debian Python tar filter."""
import os
from pathlib import Path, PurePosixPath
import posixpath
import shutil
import tarfile


def extract_tar(archive, destination, allow_links=False):
    destination = Path(destination).resolve()
    destination.mkdir(mode=0o700, parents=True, exist_ok=True)
    with tarfile.open(archive) as handle:
        members = handle.getmembers()
        if len(members) > 200000 or sum(m.size for m in members) > 4 * 1024**3:
            raise ValueError('archive exceeds extraction limits')
        names, links = set(), {}
        for member in members:
            name = member.name.rstrip('/')
            path = PurePosixPath(name)
            if (not name or path.is_absolute() or '..' in path.parts or '\\' in name or ':' in name
                    or path.as_posix() != name or name in names):
                raise ValueError('unsafe or duplicate archive path')
            names.add(name)
            if member.issym() and allow_links:
                link = member.linkname
                target = posixpath.normpath(posixpath.join(str(path.parent), link))
                if (not link or link.startswith('/') or '\\' in link or ':' in link
                        or target == '..' or target.startswith('../')):
                    raise ValueError('escaping archive link')
                links[name] = target
            elif not (member.isfile() or member.isdir()):
                raise ValueError('archive links and special files are not permitted')
        for name in names:
            if any(str(parent) in links for parent in PurePosixPath(name).parents):
                raise ValueError('archive contains a symlink parent')
        for name, target in links.items():
            if target not in names or target in links:
                raise ValueError('archive link does not name a regular member')
        for member in members:
            name = member.name.rstrip('/')
            target = destination / name
            if not target.resolve().is_relative_to(destination):
                raise ValueError('existing path escapes extraction root')
            if member.isdir():
                target.mkdir(mode=0o755, parents=True, exist_ok=True)
            elif member.isfile():
                target.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
                with handle.extractfile(member) as source, target.open('xb') as output:
                    shutil.copyfileobj(source, output)
                target.chmod(member.mode & 0o755)
        for name, target in links.items():
            path = destination / name
            path.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
            os.symlink(os.path.relpath(destination / target, path.parent), path)

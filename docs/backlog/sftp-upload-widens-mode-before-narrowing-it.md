---
worth: later
where: pkg/executor/remote.go:477
added: 2026-09-04
---
# a 0600 file is briefly readable at the remote destination during upload

`sftpUpload` creates the destination, copies every byte, and only then narrows the mode:
`sftpClient.Create(dst)` at remote.go:477, `io.Copy`, then `remoteFh.Chmod(inpFi.Mode().Perm())`
at remote.go:509. `Create` maps to the sftp `open` call with pflags only and sends no
`ATTR_PERMISSIONS` attribute (vendor/github.com/pkg/sftp/client.go:668), so the server picks
0666 masked by its own umask. Under the usual 022 that is 0644, and the file sits at 0644 for
the whole transfer before the chmod arrives.

The window only opens when the destination is new. An existing file is opened with `O_TRUNC` and
keeps whatever mode it already had, so a second push of the same file is not exposed. The sudo
path always hits it, since the staging file goes into a freshly created directory under /tmp.

This matters for a local file the operator deliberately keeps at 0600 - an `.env`, a private key,
a rendered config holding a loaded secret. Another account on the target can read it during the
copy. It applies to every push through this path: `copy`, `mcopy`, `sync`, and the `template`
command from #349.

The fix belongs here rather than in any caller: open the remote file with the intended mode
before writing, or chmod before the data copy, then apply the final mode after a successful
transfer. It has to leave the mode semantics `copy`'s callers already depend on intact, which is
why it is a change of its own rather than something to fold into a feature branch. Surfaced
during the review of #349 and confirmed against the pkg/sftp source.

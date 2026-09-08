# Security Policy

## Supported versions

The latest release receives security fixes. Older tags are not patched.

## Reporting a vulnerability

Report privately through GitHub's
[Report a vulnerability](../../security/advisories/new) form, which opens a
draft advisory only the maintainer can see. Please do not open a public issue
for a suspected vulnerability.

Include the archive or input that triggers the problem where possible, the
command line used, and the `zipshrink` version. Expect an initial response
within roughly a week; a public advisory and patched release follow once a
fix is confirmed.

## Scope

`zipshrink` parses untrusted archives and, by default, **destroys the source
archive as it extracts**. Findings of particular interest:

- reads or writes outside the destination directory (path traversal)
- crashes, panics or unbounded memory or disk use from a crafted archive
- data loss beyond the documented behaviour, especially with `-k`, which must
  never modify the source

The ZIP parser is fuzzed continuously in CI; crashing inputs are welcome
regardless of whether they look exploitable.

## Verifying a release

Release archives are accompanied by `SHA256SUMS`, a keyless
[cosign](https://github.com/sigstore/cosign) signature over it, and build
provenance. See the verification commands in [README.md](README.md).

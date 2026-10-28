# Releasing

Shadow Mock uses [semantic versioning](https://semver.org): `vMAJOR.MINOR.PATCH`, for example
`v0.2.0`. A release is made by pushing a version tag. Nothing is released by merging.

## What the numbers mean

Before 1.0, the API is not frozen. From 1.0 on, the rules below are strict.

| Part | Raised for | Before 1.0 |
|---|---|---|
| MAJOR | A change that can break an existing user | Use MINOR instead |
| MINOR | A new feature that breaks nothing | A new feature, or a change that can break a user |
| PATCH | A fix that breaks nothing | The same |

A change can break a user if it alters any of:

- how a mapping file or the admin API behaves, or what they accept,
- a configuration key, a command-line flag or an environment variable,
- the default of a setting,
- anything on [the WireMock compatibility page](COMPATIBILITY.md).

Say so in the pull request. A pre-release has a suffix: `v0.3.0-rc.1`.

## Making a release

1. Make sure `main` is green, including the `wiremock-reference` job, and that the changes since
   the last release are in. The release notes are generated from the merged pull requests.
2. Tag the commit and push the tag:

   ```bash
   git checkout main && git pull
   git tag -a v0.2.0 -m "v0.2.0"
   git push origin v0.2.0
   ```

3. The `release-binaries` workflow builds every platform, checks that the binary reports the
   version in the tag, verifies the checksums and publishes the release. A tag that is not a
   semantic version is refused. A tag with a suffix (`v0.3.0-rc.1`) is published as a pre-release.

Never move or reuse a version tag. If a release is bad, publish the next patch version. To pull a
release that must not be downloaded, delete the release and its tag, and do not use that number
again.

## Container images

The `docker` workflow publishes `ghcr.io/pwgith/shadow-mock` for `linux/amd64` and `linux/arm64`. A
stable tag `v0.2.0` gives `0.2.0`, `0.2` and `latest`; a pre-release tag gives only its own
version; every merge to `main` replaces `edge`. A pull request that changes the image builds it and
checks that it reports the right version and turns healthy, without publishing.

The first publish creates the package as private. In the repository's package settings on GitHub,
make it public so users can pull it without signing in.

## Builds of `main`

Every merge to `main` replaces one pre-release, **`edge`**, with a build of that commit. Use it to
try a change before the next release. It reports a version like `0.1.0-edge.51d241d1`: the newest
release tag, then `edge`, then the commit. It is not a stable release and is replaced without
notice.

## Installing

Download the archive for your system from the
[releases page](https://github.com/pwgith/shadow-mock/releases), check it against `SHA256SUMS`, and
unpack it. `shadowmock -version` prints the version. The binaries are not code-signed yet, so
Windows warns about an unknown publisher.

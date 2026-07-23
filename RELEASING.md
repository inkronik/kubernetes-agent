# Releasing

Releases publish a multi-platform image to
`ghcr.io/inkronik/kubernetes-agent`. GitHub Actions authenticates with the
repository's `GITHUB_TOKEN`; no long-lived registry credential is required.

## Prepare a release

1. Update `VERSION` with an exact semantic version without the `v` prefix.
2. Update the image tag and raw manifest URL in `README.md` and
   `deploy/kubernetes.yaml`.
3. Run `gofmt`, `go vet`, `go test -race`, and a Docker build.
4. Merge the changes to `main` and wait for CI to pass.
5. Create and publish a GitHub release tagged `v<VERSION>` from the release
   commit.

Publishing the GitHub release triggers the release workflow. It validates the
tag against `VERSION`, builds `linux/amd64` and `linux/arm64`, pushes immutable
semantic-version tags plus `latest`, and attaches build provenance and an SBOM.

Do not overwrite or recreate an existing version tag.

## First release and visibility

After the first workflow completes, open the package settings for
`inkronik/kubernetes-agent`, confirm that it is linked to this repository, and
set its visibility to public if it did not inherit public visibility. Customer
installation must remain blocked until an unauthenticated pull succeeds.

Verify the published index and embedded version:

```sh
docker buildx imagetools inspect ghcr.io/inkronik/kubernetes-agent:1.0.0
docker run --rm ghcr.io/inkronik/kubernetes-agent:1.0.0 --version
```

The index must contain both `linux/amd64` and `linux/arm64`, and the second
command must print the exact value from `VERSION`.

## Rollback

Roll back by pinning the Deployment to a previously verified full semantic
version and applying the manifest again. Do not move or overwrite a full
version tag. Mutable major, major/minor, and `latest` tags are conveniences and
must not be used as production rollback targets.

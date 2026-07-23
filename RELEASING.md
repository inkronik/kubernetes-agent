# Releasing

Releases publish a multi-platform image to
`ghcr.io/inkronik/kubernetes-agent` and an OCI Helm chart to
`ghcr.io/inkronik/charts/inkronik-kubernetes-agent`. GitHub Actions
authenticates with the repository's `GITHUB_TOKEN`; no long-lived registry
credential is required.

## Prepare a release

1. Update `VERSION` with an exact semantic version without the `v` prefix.
2. Set `version` and `appVersion` in
   `charts/inkronik-kubernetes-agent/Chart.yaml` to the same version.
3. Update the image tag, chart version, and raw manifest URL in `README.md`, the
   chart README, and `deploy/kubernetes.yaml`.
4. Run `gofmt`, `go vet`, `go test -race`, Helm lint/render/package checks, and
   a Docker build.
5. Merge the changes to `main` and wait for CI to pass.
6. Create and publish a GitHub release tagged `v<VERSION>` from the release
   commit.

Publishing the GitHub release triggers the release workflow. It validates the
tag, chart version, and chart app version against `VERSION`; builds
`linux/amd64` and `linux/arm64`; pushes immutable semantic-version image tags
plus `latest`; attaches image provenance and an SBOM; and publishes the matching
OCI chart only after the image succeeds.

Do not overwrite or recreate an existing version tag.

## First release and visibility

After the first workflow completes, open the package settings for both the
`inkronik/kubernetes-agent` image and the
`inkronik/charts/inkronik-kubernetes-agent` chart. Confirm that they are linked
to this repository and set their visibility to public if they did not inherit
public visibility. Customer installation must remain blocked until
unauthenticated pulls succeed.

Verify the published index and embedded version:

```sh
docker buildx imagetools inspect ghcr.io/inkronik/kubernetes-agent:1.0.0
docker run --rm ghcr.io/inkronik/kubernetes-agent:1.0.0 --version
helm pull oci://ghcr.io/inkronik/charts/inkronik-kubernetes-agent --version 1.0.0
helm template release-check \
  oci://ghcr.io/inkronik/charts/inkronik-kubernetes-agent \
  --version 1.0.0 \
  --namespace inkronik \
  --set-string clusterName=release-check
```

The index must contain both `linux/amd64` and `linux/arm64`, and the second
command must print the exact value from `VERSION`. The chart must pull without
registry credentials and render the matching full-version image.

## Rollback

For Helm installations, use `helm history` and `helm rollback` to restore a
previous release revision. For raw-manifest installations, pin the Deployment
to a previously verified full semantic version and apply the manifest again.
Do not move or overwrite a full version tag. Mutable major, major/minor, and
`latest` tags are conveniences and must not be used as production rollback
targets.

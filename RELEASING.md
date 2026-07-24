# Releasing

Releases publish a multi-platform image to
`ghcr.io/inkronik/kubernetes-agent` and an OCI Helm chart to
`ghcr.io/inkronik/charts/inkronik-kubernetes-agent`. GitHub Actions
authenticates with the repository's `GITHUB_TOKEN`; no long-lived registry
credential is required.

## Automated release

Use **Actions > Release > Run workflow**. The workflow uses `release-it` to
calculate the version, synchronize every version reference, create the release
commit and tag, publish the GitHub Release, and then publish the image and chart.

Choose these inputs:

- `branch: main` for stable releases. The first run automatically publishes the
  existing, consistent `1.0.0`; later runs calculate the next version from
  commits since the latest tag.
- `branch: rc` for prereleases. The workflow adds or advances the prerelease
  identifier and marks the artifact release as a prerelease.
- Keep `dry_run: true` for the first run. Review the log, then run the same
  inputs with `dry_run: false` to create and publish the release.

The workflow calculates versions from Conventional Commits: `fix`, `perf`, and
`revert` create a patch release; `feat` creates a minor release; and a
`BREAKING CHANGE` footer or `!` after the type creates a major release.
Documentation, test, build, CI, refactor, style, and chore-only changes do not
create a release. If there are no releasable commits, the workflow stops before
tagging or publishing.

The selected branch must exist, be up to date, and have passing CI. A version
bump pushes a `chore(release): v<VERSION>` commit and every real run pushes an
annotated `v<VERSION>` tag. During the tagless first release, the already
consistent branch tip is tagged without creating an empty commit. Do not edit
`VERSION`, chart metadata, deployment images, or documentation by hand; the
release hook updates them together and verifies consistency before the commit
is created.

The artifact stage validates the tag and chart metadata against `VERSION`,
builds `linux/amd64` and `linux/arm64`, pushes the immutable full-version image,
attaches image provenance and an SBOM, and publishes the matching OCI chart.
Stable releases also move the major, major/minor, and `latest` image tags;
prereleases do not.

The artifact workflow can still be triggered by publishing a GitHub Release
manually. The automated workflow invokes it directly because events created by
the repository `GITHUB_TOKEN` do not start another workflow run.

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

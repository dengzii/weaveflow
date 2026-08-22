# GitHub automation

## Continuous integration

`workflows/ci.yml` runs Go formatting, vet, tests and coverage, WebUI tests and production build, cross-platform Go
compilation, and a container health check for changes on `master`, pull requests, and the weekly scheduled run.

`workflows/codeql.yml` performs CodeQL analysis for Go and JavaScript/TypeScript. `workflows/security.yml` runs
`govulncheck` and reviews dependency changes in pull requests.

`workflows/codespaces-demo.yml` builds and publishes `ghcr.io/dengzii/weaveflow-codespaces:main` when files used by the
demo image change on `master`. It uses a dedicated GitHub Actions BuildKit cache scope so the Codespaces demo image
does not evict the production image cache. CI reads the stable production scope;
pull requests write an isolated `production-pr-*` scope, and releases write a tag-specific `production-release-*`
scope. The publish job runs only in the canonical `dengzii/weaveflow` repository because forks cannot write its GHCR
package. The package must be public for anonymous users to launch the one-click Codespaces profile.

`workflows/deploy-sites.yml` builds and deploys the website, the bundled English and Chinese documentation, and the
Playground to AWS EC2 after a successful `CI` run for relevant changes on `master`. It uses the protected
`production-sites` Environment, a
dedicated SSH identity with pinned host keys, and the root-owned atomic deployer documented in
[`scripts/ec2/README.md`](../scripts/ec2/README.md). Configure the EC2 host before enabling this workflow; the first run
fails closed if the deployer, Environment variables, or secrets are missing.

## Releases and Docker Hub

Configure these repository Actions secrets under **Settings > Secrets and variables > Actions**:

- `DOCKERHUB_USERNAME`: the Docker Hub account used for login and, by default, the image namespace.
- `DOCKERHUB_TOKEN`: a Docker Hub access token with permission to push the image.
- `CODECOV_TOKEN`: optional Codecov upload token; CI continues without Codecov when it is not configured.

Create a GitHub Environment named `release` and add required reviewers if publishing should require approval. Enable
Secret scanning, Push protection, and Private vulnerability reporting under the repository **Security** settings; those
controls cannot be committed as repository files.

Create the labels used by the Issue Forms and release notes (`bug`, `enhancement`, `feature`, `fix`,
`breaking-change`, `documentation`, and `skip-changelog`) if they are not already present.

The default image name is `<DOCKERHUB_USERNAME>/weaveflow`. To publish to another user, organization, or repository,
set the optional Actions repository variable `DOCKERHUB_IMAGE`, for example `my-organization/weaveflow`.

Push a semantic version tag to publish a GitHub Release and a multi-platform Docker image:

```bash
git tag -a v1.2.3 -m "Release v1.2.3"
git push origin v1.2.3
```

Stable releases publish Docker tags `1.2.3`, `1.2`, `1`, and `latest`. A prerelease tag such as `v1.2.3-rc.1`
publishes only `1.2.3-rc.1` and creates a GitHub prerelease. Every release also uploads Linux, macOS, and Windows
server archives plus `checksums.txt` and the image digest. The Release workflow can also be started manually for an
existing tag from the Actions page.

The Docker image is built for `linux/amd64` and `linux/arm64`, includes SBOM/provenance metadata, is keylessly signed
with Cosign, and receives a GitHub build attestation. Verify a stable image with:

```bash
cosign verify \
  --certificate-identity-regexp='https://github.com/dengzii/weaveflow/.github/workflows/release.yml@refs/tags/v.*' \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  "$DOCKERHUB_IMAGE@sha256:<digest>"
docker buildx imagetools inspect "$DOCKERHUB_IMAGE:1.2.3"
```

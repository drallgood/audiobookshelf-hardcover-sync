# Release Process

This document describes the process for creating and publishing new releases of the audiobookshelf-hardcover-sync project.

## Prerequisites

- GitHub CLI (`gh`) installed and authenticated
- Git access to the repository with push permissions
- Access to the repository's GitHub Actions

## Release Steps

1. **Prepare on `develop`**
   - Ensure all intended features and bugfixes are merged into `develop`
   - Make sure tests are passing (`make test`)
   - Update documentation as needed
   - Update `MIGRATION.md` if there are breaking changes

2. **Determine Version Number**
   - Follow [Semantic Versioning](https://semver.org/):
     - MAJOR version for incompatible API changes
     - MINOR version for new functionality in a backward compatible manner
     - PATCH version for backward compatible bug fixes

3. **Update CHANGELOG.md**
   - Ensure all changes are documented under the `[Unreleased]` section
   - Use the Makefile's `prepare-release` target to update the changelog:
     ```bash
     make prepare-release VERSION=vX.Y.Z
     ```
   - This will replace `## [Unreleased]` with `## [vX.Y.Z] - YYYY-MM-DD`

4. **Merge `develop` into `main`**
   - Open a PR from `develop` to `main` and merge after approvals, or fast-forward if appropriate.
   - Ensure the merge is clean and CI is green.

5. **Commit the Changelog Update (if needed)**
   ```bash
   git add CHANGELOG.md
   git commit -m 'Release vX.Y.Z'
   ```

6. **Create and Push Git Tag (on `main`)**
   ```bash
   git tag -a vX.Y.Z -m 'Release vX.Y.Z'
   git push origin main vX.Y.Z
   ```

7. **Monitor GitHub Actions Workflow**
   - Pushing the tag will automatically trigger the release workflow
   - Visit the Actions tab in the GitHub repository to monitor progress
   - The workflow will:
     - Build and test the code
     - Build and publish Docker images
     - Create a GitHub Release with notes from CHANGELOG.md

8. **Verify Release**
   - Check that the GitHub Release was created correctly
   - Verify Docker images are available at ghcr.io/drallgood/audiobookshelf-hardcover-sync

## Troubleshooting

If the automated release process fails:

1. **Check GitHub Actions Logs**
   - Look for error messages in the GitHub Actions workflow

2. **Release Permissions**
   - Ensure the GitHub workflow has the correct permissions:
     - The `create-release` job must have `contents: write` permission
     - Make sure the GitHub token has sufficient access

3. **Manual Release**
   - If GitHub Actions continue to fail, you can create a release manually:
     ```bash
     gh release create vX.Y.Z --title "Release vX.Y.Z" --notes-file CHANGELOG.md
     ```

## Post-Release

1. **Back-merge into `develop`**
   - Ensure `main` is merged back into `develop` to keep branches in sync:
     ```bash
     git checkout develop
     git pull --ff-only
     git merge --ff-only origin/main || git merge origin/main
     git push origin develop
     ```

2. **Update Documentation**
   - Add a new `## [Unreleased]` section at the top of CHANGELOG.md on `develop`
   - Commit and push this change

3. **Announce the Release**
   - Inform users through appropriate channels

## Docker Images

Docker images are automatically built and published by GitHub Actions to:
`ghcr.io/drallgood/audiobookshelf-hardcover-sync:vX.Y.Z`

Users can pull the image with:
```bash
docker pull ghcr.io/drallgood/audiobookshelf-hardcover-sync:vX.Y.Z
```

Or use the `latest` tag for the most recent release:
```bash
docker pull ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
```

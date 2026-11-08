# Releasing

1. Make sure `main` is green (`go vet ./...`, `TF_ACC=1 go test ./...`).
2. Tag a SemVer version and push it: `git tag v0.1.1 && git push origin v0.1.1`.
3. The `release` workflow builds all platforms with GoReleaser, signs `SHA256SUMS` with the
   project GPG key (repository secrets `GPG_PRIVATE_KEY`, `PASSPHRASE`) and creates the GitHub release.
4. The Terraform Registry picks up the new release automatically.

Breaking schema changes need a major version bump and a state upgrader.

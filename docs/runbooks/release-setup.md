# Release Setup Runbook

Required GitHub Actions secrets for the release workflow (`.github/workflows/release.yml`).

## Secrets

### 1. APPLE_DEVELOPER_ID_APPLICATION_CERT

Base64-encoded PEM certificate for the "Developer ID Application" identity.

- Obtain from Apple Developer portal > Certificates, Identifiers & Profiles > Developer ID Application.
- Export the certificate from Keychain Access as `.p12`, convert to PEM: `openssl pkcs12 -in cert.p12 -clcerts -nokeys -out cert.pem`
- Encode: `base64 < cert.pem | pbcopy`
- Store as GitHub Actions secret `APPLE_DEVELOPER_ID_APPLICATION_CERT`.

### 2. APPLE_DEVELOPER_ID_APPLICATION_KEY

Base64-encoded PEM private key corresponding to the certificate above.

- Export from Keychain or extract from `.p12`: `openssl pkcs12 -in cert.p12 -nocerts -nodes -out key.pem`
- Encode: `base64 < key.pem | pbcopy`
- Store as GitHub Actions secret `APPLE_DEVELOPER_ID_APPLICATION_KEY`.

### 3. APPLE_API_KEY

Base64-encoded `.p8` App Store Connect API key file used for notarization.

- Create at App Store Connect > Users and Access > Integrations > App Store Connect API > Keys.
- Download the `.p8` file (available only once).
- Encode: `base64 < AuthKey_XXXXXXXXXX.p8 | pbcopy`
- Store as GitHub Actions secret `APPLE_API_KEY`.

### 4. APPLE_API_KEY_ID

The 10-character Key ID shown next to the API key in App Store Connect.

- Copy the Key ID string directly.
- Store as GitHub Actions secret `APPLE_API_KEY_ID`.

### 5. APPLE_API_ISSUER

The Issuer ID (UUID) shown at the top of the App Store Connect API Keys page.

- Copy the Issuer UUID directly.
- Store as GitHub Actions secret `APPLE_API_ISSUER`.

### 6. HOMEBREW_TAP_GITHUB_TOKEN

A GitHub personal access token (classic or fine-grained) with `repo` scope on the `yolo-labz/homebrew-tap` repository.

- Create at GitHub > Settings > Developer Settings > Personal access tokens.
- Fine-grained: grant Contents read/write on `yolo-labz/homebrew-tap`.
- Store as GitHub Actions secret `HOMEBREW_TAP_GITHUB_TOKEN`.

## Verification

After storing all secrets, push a test tag to verify the workflow:

```bash
git tag v0.0.0-test && git push origin v0.0.0-test
# Check Actions tab — the workflow should start and fail gracefully
# if secrets are placeholders.
git push origin --delete v0.0.0-test && git tag -d v0.0.0-test
```

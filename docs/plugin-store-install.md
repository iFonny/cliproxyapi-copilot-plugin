# Install from the plugin store

This is the low-effort path for an existing CLIProxyAPI deployment, including a
NAS running the official Docker image. Nothing is compiled and no file is
copied by hand: CLIProxyAPI downloads the release itself, verifies it against
the published `checksums.txt`, and writes the library into its own plugin
directory.

Everything below happens once. Afterwards, upgrading to a newer plugin release
is a single click in the management center.

## 1. Publish a release from the fork

The plugin store installs from GitHub releases, so a release has to exist.

GitHub disables Actions on new forks, which also disables this repository's
release workflow. Enable it first, under **Settings → Actions → General →
Actions permissions → Allow all actions and reusable workflows**, then save.

Confirm it took effect:

```bash
gh api repos/iFonny/cliproxyapi-copilot-plugin/actions/workflows \
  --jq '.workflows[].name'
```

The output should list `CI` and `Release`. While it prints nothing, no workflow
can run and no release will be published.

Then merge the changes to `main` and push a version tag:

```bash
git checkout main
git pull origin main
git tag v0.4.0
git push origin v0.4.0
```

The release workflow runs the tests, builds inside `golang:1.26-bookworm`, and
publishes two assets:

```text
cliproxyapi-copilot-openai_0.4.0_linux_amd64.zip
checksums.txt
```

Both names matter. CLIProxyAPI looks for exactly
`<plugin-id>_<version>_<goos>_<goarch>.zip` next to a `checksums.txt`, and
expects `cliproxyapi-copilot-openai.so` at the root of the archive.

## 2. Register the personal plugin store

Add the registry URL to the deployment's `config.yaml`. The official registry
is always included, so this only adds a second source:

```yaml
plugins:
  enabled: true
  dir: "/CLIProxyAPI/plugins"
  registries:
    - "https://raw.githubusercontent.com/iFonny/cliproxyapi-copilot-plugin/main/registry.json"
```

There must be only one top-level `plugins` key; merge the `registries` list
into the existing block rather than replacing it. Keep any entries already
present under `plugins.configs`.

Restart CLIProxyAPI so it reloads the configuration:

```bash
docker compose up -d --force-recreate cliproxyapi
```

## 3. Install the plugin

Open the management center, go to the plugin store, and install **GitHub
Copilot subscription provider (OpenAI-compatible)**. The store resolves the
latest release tag on its own.

Or from a shell, using the deployment's management password:

```bash
curl -fsS \
  -H "Authorization: Bearer $MANAGEMENT_PASSWORD" \
  http://127.0.0.1:8317/v0/management/plugins
```

The installed entry should report the ID `cliproxyapi-copilot-openai`.

## 4. Merge the plugin configuration

The store installs the library but does not configure it. Add the plugin's
settings block, then restart once more:

```yaml
plugins:
  enabled: true
  dir: "/CLIProxyAPI/plugins"
  registries:
    - "https://raw.githubusercontent.com/iFonny/cliproxyapi-copilot-plugin/main/registry.json"
  configs:
    cliproxyapi-copilot-openai:
      enabled: true
      priority: 100
      github_client_id: "Iv1.b507a08c87ecfe98"
      github_scope: "read:user"
      github_base_url: "https://github.com"
      github_api_url: "https://api.github.com"
      copilot_api_url: "https://api.githubcopilot.com"
      oauth_timeout_seconds: 900
      model_cache_ttl_seconds: 600
      token_expiry_buffer_seconds: 300
      excluded_model_prefixes: []
```

Set `excluded_model_prefixes` to `["claude-"]` if the deployment also uses
CLIProxyAPI's native Claude subscription provider, so Claude model IDs are not
scheduled through Copilot as well.

## 5. Authenticate

The auth provider key is `copilot` and does not depend on the plugin ID. A
deployment that previously ran the upstream plugin therefore keeps its stored
credential and needs no new GitHub login.

Otherwise start the **Copilot** login in the management center and complete
GitHub's device-code flow.

## 6. Replacing an earlier build

The plugin ID comes from the library filename, so this build and a previously
installed `cliproxyapi-copilot.so` are two distinct plugins and will both load.
Remove the old one to avoid scheduling the same Copilot models twice:

1. Delete the old `cliproxyapi-copilot.so` from the plugin directory.
2. Delete the old `cliproxyapi-copilot` key from `plugins.configs`.
3. Restart CLIProxyAPI.

Leave the Copilot auth entry alone; the new plugin reuses it.

## Upgrading later

Push a new version tag, wait for the release workflow, then use the plugin
store's upgrade action. The store re-verifies the checksum before replacing the
library.

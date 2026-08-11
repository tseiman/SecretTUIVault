# Maintenance scripts

## Repair legacy CR-only blobs

Run from the SecretTUIVault repository root:

```console
go run ./scripts/repair-secretvault-cr.go /absolute/path/to/vault.yaml
```

The script only changes scalar values whose YAML key is `blob`:

1. CRLF line endings become LF.
2. Remaining CR line endings become LF.
3. Affected blobs are emitted as YAML literal blocks (`blob: |`).

Before replacing the vault, the script writes the original bytes with the same file permissions to:

```text
vault.yaml.before-cr-repair
```

It refuses to overwrite an existing backup. If no affected blob exists, it leaves the vault unchanged and creates no backup.

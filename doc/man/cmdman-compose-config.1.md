# cmdman-compose-config(1)

## Name

`cmdman compose config` - validate and render canonical compose configuration

## Synopsis

```text
cmdman compose [selection flags] config
```

## Description

Loads the selected compose file, applies interpolation, resolves work and
command directories, merges `env_file` and `env`, validates dependency and
runtime fields, and prints canonical YAML.

The output is a valid compose file that produces the same desired plan when fed
back to cmdman. It is useful for reviewing interpolation and path resolution
before `up`, and for diagnosing why a configuration hash changed.

Canonical environment output contains explicitly declared command environment,
not the entire host environment used during interpolation.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `--workdir`.

## Examples

```sh
cmdman compose config
cmdman compose -f ./deploy/cmd-compose.yaml config > resolved.yaml
```

## See Also

[cmdman-compose(1)](./cmdman-compose.1.md), [cmdman-compose-create(1)](./cmdman-compose-create.1.md)

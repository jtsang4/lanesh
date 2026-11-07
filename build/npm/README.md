# lanesh

Share skills and shells across your machines through one MCP endpoint.

This package installs the `lanesh` command. On first run it downloads the
native binary for your platform from
[GitHub Releases](https://github.com/jtsang4/lanesh/releases), verifies its
SHA-256 checksum, and stores it in `~/.lanesh/bin` (override with
`LANESH_HOME`).

```sh
npx lanesh --help
# or
npm install -g lanesh
```

See the [project README](https://github.com/jtsang4/lanesh#readme) for details.

# __PLUGIN_NAME__

TODO: describe what this fylr plugin does.

## Building

Built by [fylr-build-plugin](https://github.com/programmfabrik/fylr-build-plugin) —
see there for how a fylr plugin is structured and what it can offer; the
Makefile is a thin shim, run `make` for the target list. For development,
point the fylr server at the build folder in `fylr.yml`:

```yaml
plugin:
    paths+:
        - ../__PLUGIN_NAME__/build
```

## Releasing

A manager publishes a release on the GitHub release page (tag `vX.Y.Z`); the
workflow builds the plugin zip and attaches it to the release. It also puts the
zip on GitHub Pages as `<repo>-prerelease.zip`, the install url for testing a
prerelease. Once per repository: Settings > Pages > Source = GitHub Actions,
then Settings > Environments > github-pages > add the deployment tag rule
`v*.*.*`, or the release tags may not deploy.

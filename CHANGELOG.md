# Changelog

## [1.3.0](https://github.com/KronosWasTaken/zipshrink/compare/v1.2.0...v1.3.0) (2026-09-13)


### Features

* **progress:** report both archive and output throughput ([e39538a](https://github.com/KronosWasTaken/zipshrink/commit/e39538a0c8b517255604f48fee49e79e11a960f3))

## [1.2.0](https://github.com/KronosWasTaken/zipshrink/compare/v1.1.0...v1.2.0) (2026-09-13)


### Features

* show extraction progress ([85dc2bf](https://github.com/KronosWasTaken/zipshrink/commit/85dc2bf76cf570ed87b730140cd79b5eea8aba6d))


### Bug Fixes

* **ci:** pin cosign-installer to an existing version tag ([ee38927](https://github.com/KronosWasTaken/zipshrink/commit/ee3892770110a0bd01236d9365e4639813d578cf))
* **rar:** stop dropping match bytes at the decode window wrap ([7f36bb8](https://github.com/KronosWasTaken/zipshrink/commit/7f36bb8d7b6e2fc91c9806d9bf00e2710e828551))

## [1.1.0](https://github.com/KronosWasTaken/zipshrink/compare/v1.0.3...v1.1.0) (2026-09-13)


### Features

* reclaim space by punching holes and decode entries in parallel ([c54845c](https://github.com/KronosWasTaken/zipshrink/commit/c54845c1a440a5737adff027711aee8095acb2c6))


### Bug Fixes

* bound zero-length entries so directories do not consume the archive ([6bbff37](https://github.com/KronosWasTaken/zipshrink/commit/6bbff3708b3d0b6f1e392095b3ee9b355cd700af))

## [1.0.3](https://github.com/KronosWasTaken/zipshrink/compare/v1.0.2...v1.0.3) (2026-09-08)


### Bug Fixes

* **ci:** grant attestations permission to the release workflow call ([6038fc0](https://github.com/KronosWasTaken/zipshrink/commit/6038fc0ae39605b1d724392ce43798fd72aebe77))

## [1.0.2](https://github.com/KronosWasTaken/zipshrink/compare/v1.0.1...v1.0.2) (2026-09-08)


### Bug Fixes

* **ci:** attach release assets when release-please cuts a release ([bc2a267](https://github.com/KronosWasTaken/zipshrink/commit/bc2a2672811ddbc67cd6b22243ca8ffe58066000))

## [1.0.1](https://github.com/KronosWasTaken/zipshrink/compare/v1.0.0...v1.0.1) (2026-09-08)


### Bug Fixes

* **ci:** release workflow never triggered on tags ([7856c1e](https://github.com/KronosWasTaken/zipshrink/commit/7856c1e94fcc453fcfad2fc6500bd87ae7ddaa2d))

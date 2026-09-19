# Changelog

## [0.7.0](https://github.com/sschueller/brother-ql570-go/compare/v0.6.1...v0.7.0) (2026-09-19)


### Features

* **barcode:** add optional HRI text below Code128 ([5c50776](https://github.com/sschueller/brother-ql570-go/commit/5c50776f28524979710f39483f3bfa0c2b8cd868))
* **render:** support vertical alignment on fixed-length labels ([74d2cf0](https://github.com/sschueller/brother-ql570-go/commit/74d2cf09e1b2b4e66520c6daa07d5f7cee41cc96))
* **web:** add fixed label length option for continuous tape ([00cbe5d](https://github.com/sschueller/brother-ql570-go/commit/00cbe5d714ce1befadcf061061c89d51e7f9e394))


### Bug Fixes

* **ql:** add Code128 checksum and quiet zone ([e33ee6e](https://github.com/sschueller/brother-ql570-go/commit/e33ee6e3686b04dc0786f6c61495a45a5bb44bf4))

## [0.6.1](https://github.com/sschueller/brother-ql570-go/compare/v0.6.0...v0.6.1) (2026-09-18)


### Bug Fixes

* **pdf:** release the warmed instance so uploads can borrow it ([a3f138a](https://github.com/sschueller/brother-ql570-go/commit/a3f138a68b2c3aea7bdc4725a1d14f424f3ef82f))


### Performance Improvements

* **pdf:** warm the PDF engine at daemon startup and reuse the WASM worker ([c18f9de](https://github.com/sschueller/brother-ql570-go/commit/c18f9debc5cf6690f7f8f802ea3c14ab3a198d5c))

## [0.6.0](https://github.com/sschueller/brother-ql570-go/compare/v0.5.0...v0.6.0) (2026-09-09)


### Features

* **ql:** add image_trim option to crop white margins around images ([a6f1b04](https://github.com/sschueller/brother-ql570-go/commit/a6f1b04be144a53555f5cfee49f0c560e9d413d6))
* **ql:** fill the label width when rotating images 90/270 degrees ([98b7f9f](https://github.com/sschueller/brother-ql570-go/commit/98b7f9fa4868ec6d14f10b9f32bdb40f614d408b))


### Bug Fixes

* **pdf:** respect bitmap row stride when copying rendered pages ([26e1005](https://github.com/sschueller/brother-ql570-go/commit/26e10053cc361b4a0658488b0afa92dcd7bda9c2))
* **ql:** scale rotated content to fit the printable width instead of rejecting it ([d300b8b](https://github.com/sschueller/brother-ql570-go/commit/d300b8bfbc621c7aae73b97ad6552bd50f398c84))

## [0.5.0](https://github.com/sschueller/brother-ql570-go/compare/v0.4.1...v0.5.0) (2026-09-09)


### Features

* add PDF upload/print and image fit ([d844c15](https://github.com/sschueller/brother-ql570-go/commit/d844c15e644c6d825daa5c8373487ae5137e8d9d))

## [0.4.1](https://github.com/sschueller/brother-ql570-go/compare/v0.4.0...v0.4.1) (2026-09-09)


### Performance Improvements

* **ql:** cache fonts and auto-grow canvas ([aa17820](https://github.com/sschueller/brother-ql570-go/commit/aa178209f947e0d0b31dd70623ec219b76fc114c))

## [0.4.0](https://github.com/sschueller/brother-ql570-go/compare/v0.3.0...v0.4.0) (2026-09-06)


### Features

* **ql:** add horizontal margin support ([9adfdf7](https://github.com/sschueller/brother-ql570-go/commit/9adfdf7ff2d5fbb49077654a5b3ebcb87726797e))

## [0.3.0](https://github.com/sschueller/brother-ql570-go/compare/v0.2.0...v0.3.0) (2026-09-06)


### Features

* **web:** add brace expansion for series labels ([2c71c31](https://github.com/sschueller/brother-ql570-go/commit/2c71c312d710834290ad1d0fc42fa5b72c651a85))

## [0.2.0](https://github.com/sschueller/brother-ql570-go/compare/v0.1.1...v0.2.0) (2026-09-06)


### Features

* **web:** implement dark mode support ([626b8bd](https://github.com/sschueller/brother-ql570-go/commit/626b8bdff912b5c65af3b68d683172ad97f5baab))

## [0.1.1](https://github.com/sschueller/brother-ql570-go/compare/v0.1.0...v0.1.1) (2026-09-06)


### Bug Fixes

* **release:** update branch target and add dev guides ([34427ff](https://github.com/sschueller/brother-ql570-go/commit/34427ffe0083a40b55f422e9f460bfb06c3c2d02))

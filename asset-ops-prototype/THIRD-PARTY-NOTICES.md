# Third party references and assets

## Vue Admin Better

Reference website: https://vuejs-core.cn/vue-admin-better/
Source repository: https://github.com/zxwk1998/vue-admin-better
License: MIT. Copyright (c) 2020 good luck. Full text: `assets/admin/vue-admin-better-LICENSE.txt`.

The layout, visual tokens, component treatment, and theme preview structure are adapted for the Qiyun asset/application management prototype. The application logic remains this project's implementation. The source product name, logo, advertisements, payment links and example records are not presented as Qiyun features.

Reference avatar source: https://gcore.jsdelivr.net/gh/zxwk1998/image/avatar/avatar_1.png . Stored locally as `assets/admin/avatar.png`; resized preview derivative: `avatar-small.jpg`. Included only as the reference site's demo avatar, not as an identity or endorsement.

## Font Awesome Free 5

Icons by Font Awesome: https://fontawesome.com/ . Icons are CC BY 4.0; library code is MIT; fonts are SIL OFL 1.1. Full license notice: `assets/admin/Font-Awesome-LICENSE.txt`.

Some icons were extracted from the reference page's rendered Font Awesome SVGs. Additional solid icons are from the official 5.15.4 repository: https://github.com/FortAwesome/Font-Awesome/tree/5.15.4/svgs/solid . Icons are embedded as CSS masks without changing their path geometry.

## xterm.js

`@xterm/xterm` 6.0.0 and `@xterm/addon-fit` 0.11.0 are distributed under the MIT License. The browser bundles are stored under `vendor/xterm`; the upstream license is stored at `vendor/xterm/LICENSE`.

## trzsz.js（当前未启用）

`trzsz` 1.1.6 is distributed under the MIT License. The browser bundle is kept at `vendor/trzsz/trzsz.js`; the upstream license is at `vendor/trzsz/LICENSE`.

**状态：已下线。** `index.html` 不再加载该库，服务端也已删除向远端安装 `trz`/`tsz` 的安装器；文件传输改由 Go SFTP 分块接口（`internal/transfer`）完成。保留此文件与许可声明仅为后续重新设计时可直接复用，不构成当前运行依赖。

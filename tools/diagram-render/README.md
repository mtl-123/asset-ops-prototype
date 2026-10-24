# Go 图表生成工具

工作区根目录的三张已评审 SVG 现在是图表内容的权威源文件。本工具替代原有 Python/Pillow 脚本，负责：

- 校验 SVG、高清 PNG、预览 PNG 和 HTML 查看器是否齐全；
- 从现有 SVG 重新生成可缩放 HTML 查看器；
- 使用 Chrome/Chromium 将 SVG 栅格化为 2 倍尺寸 PNG；
- 使用 Go 标准库生成受尺寸约束的预览 PNG；
- 更新审计思维导图的生成记录。

工具是独立 Go module，不影响 `asset-ops-prototype`。

## 校验现有产物

```powershell
cd tools/diagram-render
go test ./...
go run . -mode validate -source ../..
```

## 重新生成

在 Windows 上：

```powershell
go run . -mode render -source ../.. -out ../..
```

在 WSL 中可执行测试和现有产物校验：

```bash
cd /mnt/e/资产管理系统/tools/diagram-render
go test ./...
go run . -mode validate -source ../..
```

完整 PNG 重建需要当前 Go 进程可直接启动同一操作系统的 Chrome/Chromium。Windows 已安装 Chrome，因此从 PowerShell 执行上面的 Windows 重建命令；若要在 WSL 内重建，请先安装 Linux Chromium，再用 `-chrome` 指定其 Linux 路径。工具不会跨 WSL 直接调用 Windows Chrome，以免 Unicode 输出路径被互操作层转码。

为避免误覆盖，迁移验证可输出到临时目录：

```powershell
go run . -mode render -source ../.. -out "$env:TEMP\qiyun-diagram-check"
```

SVG 中包含全部图形和文字，因此无需 Python、Pillow 或重新维护两套坐标逻辑。高清 PNG 固定为 SVG 宽高的 2 倍；HTML 继续提供缩放、适应窗口及 SVG/PNG 下载。

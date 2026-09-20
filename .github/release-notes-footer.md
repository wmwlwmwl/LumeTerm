## 产物下载
- Linux 便携版 ([LumeTerm-__VERSION__-linux-amd64](https://github.com/wmwlwmwl/LumeTerm/releases/download/v__VERSION__/LumeTerm-__VERSION__-linux-amd64))：即下即用
- Linux DEB 安装包 ([LumeTerm-__VERSION__-linux-amd64.deb](https://github.com/wmwlwmwl/LumeTerm/releases/download/v__VERSION__/LumeTerm-__VERSION__-linux-amd64.deb))：Debian/Ubuntu 安装包
- Linux RPM 安装包 ([LumeTerm-__VERSION__-linux-amd64.rpm](https://github.com/wmwlwmwl/LumeTerm/releases/download/v__VERSION__/LumeTerm-__VERSION__-linux-amd64.rpm))：Red Hat/Fedora 安装包
- macOS ARM 版 ([LumeTerm-__VERSION__-macos-arm64.dmg](https://github.com/wmwlwmwl/LumeTerm/releases/download/v__VERSION__/LumeTerm-__VERSION__-macos-arm64.dmg))：Apple Silicon (M1/M2/M3/M4)
- macOS Intel 版 ([LumeTerm-__VERSION__-macos-amd64.dmg](https://github.com/wmwlwmwl/LumeTerm/releases/download/v__VERSION__/LumeTerm-__VERSION__-macos-amd64.dmg))：Intel 芯片
- Windows 便携绿色版 ([LumeTerm-__VERSION__-windows-amd64-portable.exe](https://github.com/wmwlwmwl/LumeTerm/releases/download/v__VERSION__/LumeTerm-__VERSION__-windows-amd64-portable.exe))：内嵌 WebView2，即下即用
- Windows 标准安装版 ([LumeTerm-__VERSION__-windows-amd64-installer.exe](https://github.com/wmwlwmwl/LumeTerm/releases/download/v__VERSION__/LumeTerm-__VERSION__-windows-amd64-installer.exe))：NSIS 安装包
- 每个产物附带 .sha256 校验文件，自动更新时校验 SHA256 确保文件完整性
- 本 Release **仅 Desktop**，Android 端见 [Lumin-SSH-Android](https://github.com/wmwlwmwl/Lumin-SSH-Android/releases)
- 许可见仓库 [LICENSE](LICENSE)

## 安装/卸载方法
### DEB 包（Debian/Ubuntu）
```bash
# 安装
sudo dpkg -i LumeTerm-__VERSION__-linux-amd64.deb
# 卸载
sudo dpkg -r lumeterm
```

### RPM 包（Red Hat/Fedora）
```bash
# 安装
sudo rpm -ivh LumeTerm-__VERSION__-linux-amd64.rpm
# 卸载
sudo rpm -e lumeterm
```

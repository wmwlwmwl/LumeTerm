# 桌面端发版

[English](RELEASE_EN.md)

推 `v*` 标签后，Actions（[release.yml](workflows/release.yml)）自动：生成更新日志 → 打全平台包 → 资产齐套后发布。

## 发版前

1. `main` 干净，要发的功能已合入
2. 版本号四处一致（支持 `x.y.z` 或 `x.y.z.w`）：
   - `frontend/package.json` → `version`
   - `frontend/package-lock.json` → 根与 `packages[""].version`（改完 `package.json` 后在 `frontend` 执行 `npm install --package-lock-only`）
   - `frontend/src/config.ts` → `APP_VERSION`
   - `wails.json` → `info.productVersion`
   
   > CI 打包版本以 `wails.json` / `APP_VERSION` 为准；lock 根版本不进安装包，但应与 `package.json` 对齐，避免脏 diff / 以后 `npm ci` 别扭。  
   > `npm version` **只认严格 semver**（`x.y.z`）。四段号如 `1.2.2.1` 请手改 + `--package-lock-only`，不要用 `npm version`。
3. commit message 写清用户可见改动（更新日志从「上一 tag..当前 tag」自动生成，会跳过 merge 与「发行 v…」类提交）

## 发版

```bash
# 1. 提交版本号与代码
git add -A
git commit -m "feat: 发行 v1.2.3"

# 2. 打 tag 并推送（触发打包）
git tag v1.2.3
git push origin main
git push origin v1.2.3
```

或对 Claude 说：**发行 1.2.3**（会按上面步骤 bump / commit / tag / push）。

## 发版后

1. 看 Actions：https://github.com/wmwlwmwl/LumeTerm/actions  
2. 看 Release：标题 `LumeTerm v…`、更新日志、14 个资产（含 `.sha256`）  
3. 缺资产时 job 会失败并**保持 draft**，不会半套包变成 latest

## 手动重跑

Actions → Build and Release → Run workflow → 填已有 tag（如 `v1.2.3`）。

注意：`create-draft` 会把该 tag 的 Release **改回 draft** 再传包。已对用户公开的正式版慎用重跑；需要时先确认可短暂下架。
## 说明从哪来

| 部分 | 来源 |
|------|------|
| 更新日志 | 上一 `v*` tag → 当前 tag 的 commit subject |
| 产物下载 / 安装卸载 | [release-notes-footer.md](release-notes-footer.md)（`__VERSION__` 自动替换） |

固定文案只改 footer；不要改 workflow 里塞 bullet。

## 不要

- 未推代码就打 tag  
- 四处版本号不一致  
- 强行把半失败的 draft 标成 latest  
- 为消 warning 乱升无关依赖

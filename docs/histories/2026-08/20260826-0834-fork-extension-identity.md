## [2026-08-26 08:34] | Task: 分叉扩展身份——放弃上游 Web Store 正式版,改用自托管 keyed 扩展

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> 扩展我之前安装的是正式版(Web Store),现在跑不了新代码;没有商店私钥,
> 且本项目和上游已分叉很大,应该放弃正式版。

### 🛠 Changes Overview

**Scope:** `apps/chrome-extension/manifest.json`、`.gitignore`、`skills/open-browser-use/references/installation.md`

**Key Actions:**

- **[manifest key]**: 生成 fork 专属 RSA-2048 签名密钥(`keys/extension-key.pem`,`chmod 600`,
  已加入 `.gitignore`),把公钥 DER 的 base64 写入 `manifest.json` 的 `key` 字段。
  扩展 ID 由公钥推导(`extensionIDFromPublicKey`),与本机目录路径、上游
  store/beta ID 全部解耦,永久稳定(私钥不丢、key 不改就不变)。
  当前派生 ID:`ldnahhefbkmhjohgiamfnfppaooejmag`(与主程序算法交叉核对一致)。
- **[gitignore]**: 增加 `keys/`,防止私钥入库。
- **[安装文档]**: installation.md 新增 "Self-Hosted Fork / Load Unpacked" 一节:
  一次性流程(生成 key 并 pin 进 manifest → Load unpacked → 用
  `install-manifest --extension-id <fork-id>` 注册 native host → 重启 Chrome),
  并注明禁用旧 store/beta 副本以避免双 socket。
- **[未入库]**: 私钥只存在本机 `keys/extension-key.pem`,不入库、不进聊天。

### 🧠 Design Intent (Why)

上游商店版没有新功能且拿不到其私钥,继续跟进商店路径没有意义。分叉需要
一个属于自己的稳定扩展 ID:把公钥写进 manifest 是最轻的做法(一行字段),
Load unpacked 即可得到确定性 ID,零发布、零审核、与上游彻底分离;
native host 注册随之指向该 ID,CLI 的 socket 路由保持单一确定。

### 📁 Files Modified

- `apps/chrome-extension/manifest.json`
- `.gitignore`
- `skills/open-browser-use/references/installation.md`
- `docs/histories/2026-08/20260826-0834-fork-extension-identity.md`（本文件）
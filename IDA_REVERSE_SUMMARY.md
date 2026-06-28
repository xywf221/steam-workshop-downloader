# SteamCMD Workshop Download — IDA 逆向分析总结

## 概述

对 `steamcmd.exe` / `steamclient64.dll` 的 IDA 逆向分析，目标是实现纯 Python（无外部进程调用）的 Workshop 下载器。

**核心发现**: `steamclient64.dll` 本身包含所有解压缩逻辑（VZa、VSZa、gzip、ZIP、raw LZMA），无需依赖 Steam 客户端服务进程。通过 `ctypes` 可直接调用其内部解压函数。

---

## 两种关键压缩格式

### 1. 旧格式: VZa (已完整逆向)

**用途**: 早期 Steam 的 depot chunk 压缩

**二进制结构**:
```
偏移    长度    内容
0       3       "VZa" 魔数
3       4       CRC32 (未压缩数据)
7       5       LZMA1 属性 (lc, lp, pb, dict_size)
12      n-21    LZMA 压缩数据
n-10    4       CRC32 (解压后数据)
n-6     4       原始数据大小
n-2     2       "zv" 尾部
```
头部 = 12 字节, 尾部 = 10 字节 → 总开销 22 字节

**解压算法**: 标准 LZMA1 (lzma.FORMAT_RAW + FILTER_LZMA1)，使用自定义 LZMA 属性

**Python 实现**:
```python
props = data[7:12]  # LZMA properties at offset 7
filt = lzma._decode_filter_properties(lzma.FILTER_LZMA1, props)
result = lzma.LZMADecompressor(lzma.FORMAT_RAW, filters=[filt]).decompress(data[12:-9])
```

---

### 2. 新格式: VSZa (完整逆向)

**用途**: 新版 Steam depot chunk 压缩，于 2025-2026 年左右引入

**二进制结构**:
```
偏移    长度    内容
0       4       "VSZa" 魔数
4       4       CRC32 (原始未压缩数据)
8       n-23    Steam 自定义压缩数据
n-15    4       CRC32 (重复，同 [4:8])
n-11    4       原始大小 (低 32 位)
n-7     4       原始大小 (高 32 位, <4GB 时为 0)
n-3     3       "zsv" 尾部
```
头部 = 8 字节, 尾部 = 15 字节 → 总开销 23 字节

**⚠️ 最重要的逆向发现**: VSZa **不是** LZMA 压缩！

之前我们最大的弯路是试图用 LZMA 的各种参数组合去解压 VSZa 数据（穷举 props offset、字典大小等），全部失败。原因在于 Valve 换了一种**完全自研的压缩算法**，不在标准 LZMA 范畴内。这个算法位于 `sub_138E86360`，通过 Steam 自定义的上下文管理器（`sub_138E83690` 初始化，包含 CPUID 检测、自定义 bitstream 处理）来解压。

**Python 调用方式**:
```python
import ctypes
dll = ctypes.CDLL("steamclient64.dll")
fn = ctypes.CFUNCTYPE(ctypes.c_int, ctypes.c_void_p, ctypes.c_int,
                       ctypes.c_void_p, ctypes.c_int)(dll._handle + 0xE86360)

compressed = vsza_data[8:-15]    # 去除头部 8B 和尾部 15B
expected_size = struct.unpack("<I", vsza_data[-11:-7])[0]
expected_crc  = struct.unpack("<I", vsza_data[-15:-11])[0]

out = ctypes.create_string_buffer(expected_size)
ret = fn(out, expected_size, compressed, len(compressed))
# ret == expected_size 表示成功
```

---

## 关键函数

### 在 steamclient64.dll 中

所有函数基于 ImageBase = `0x138000000` (IDA 加载基址 `0x138001000`)

| 函数名 | RVA | IDA 地址 | 大小 | 用途 |
|--------|-----|---------|------|------|
| `sub_138CECC90` | **0xCC90** | `0x138CECC90` | 407 B | **VSZa 压缩函数**: 写入 VSZa 魔数、CRC、调用 Steam 压缩 ≥ 写 zsv 尾部 |
| `sub_138CEAA90` | **0xAA90** | `0x138CEAA90` | 大 | **多格式 chunk 解压调度器**: 自动识别 5 种格式并派发 |
| `sub_138E86360` | **0x86360** | `0x138E86360` | 中等 | **VSZa 核心解压**: 纯 buffer-to-buffer 解压，可直接通过 ctypes 调用 |
| `sub_138CEA170` | 0xA170 | `0x138CEA170` | 大 | **VZa 解压 + 文件格式识别**: 检查 gzip/VZa/ZIP/raw LZMA 四种格式 |
| `sub_138CEC720` | 0xC720 | `0x138CEC720` | - | LZMA 解压包装器 (被 VZa 解压调用) |
| `sub_1384EEC80` | 0xEC80 | `0x1384EEC80` | 570 B | **DecryptAndUncompressChunk**: 解密 + 解压完整流程 |
| `sub_1384EE840` | 0xE840 | `0x1384EE840` | 866 B | **CompressAndEncryptChunk**: 压缩 + 加密完整流程 |
| `sub_1389A7B90` | 0x7B90 | `0x1389A7B90` | 672 B | **Chunk 下载 + 解压**: 构建 CDN URL → 下载 → 解压 → 重命名 |

### 在 steamconsole64.dll 中

| 函数名 | IDA 地址 | 用途 |
|--------|---------|------|
| `sub_180071140` | `0x180071140` | `workshop_download_item` 命令注册 |
| `sub_18008F390` | `0x18008F390` | 命令处理器，通过 Steam API IPC 触发下载 |

### 在 steamclient64.dll 中的 Steam API 接口

| 地址 | 用途 |
|------|------|
| `0x1384f0250` | `Steam_CreateSteamPipe` (DLL 导出) |
| `0x1384f0220` | `Steam_CreateLocalUser` (DLL 导出) |
| `0x1384f05e0` | `Steam_LogOn` (DLL 导出) |
| `0x138735e00` | ISteamUGC::DownloadItem (vtable index 52) |

---

## 解压调度器 (sub_138CEAA90) 内部逻辑

```c
int sub_138CEAA90(uint8_t* data, int len, CUtlBuffer* out, int max_size, int* format) {
    if (len < 2) goto ERROR;
    
    // 格式判断树:
    if (data[0] == 0x1F && data[1] == 0x8B) {        // gzip
        *format = 2;
        return gzip_decompress(data, len, out, max_size);
    }
    if (data[0] == 'V' && data[1] == 'Z') {           // VZa 旧格式
        *format = 5;
        return vza_decompress(data, len, out, max_size);
    }
    if (data[0] == 'V' && data[1] == 'S' && data[2] == 'Z') {  // VSZa 新格式
        *format = 7;
        return vsza_decompress(data, len, out, max_size);
    }
    if (data[0] == 'P' && data[1] == 'K') {            // ZIP
        *format = 4;
        return zip_decompress(data, len, out, max_size);
    }
    if ((data[0] & 0xF) == 8 && (data[1] | (data[0] << 8)) % 31 == 0) {  // raw LZMA
        *format = 3;
        return raw_lzma_decompress(data, len, out, max_size);
    }
    
    *format = 0;
    return 2;  // 未知格式
}
```

---

## 逆向过程中的关键弯路

### ❌ 弯路 1: 认为 steamclient64.dll 不含 VSZa 解压代码

最初在 DLL 的 `.text` 段搜索字节序列 `56 53 5A 61` ("VSZa") 没有结果。这是因为 IDA 的 `find_bytes` 等搜索工具的 API 调用有误（Python 版本兼容问题），不是 DLL 真的缺少 VSZa 支持。

实际上 VSZa 字符串在 `.rdata` 段偏移 `0x1393ba414`，被 `sub_138CECC90`（压缩函数）引用。解压函数 `sub_138CEAA90` 用的是内联字节比较 (`'V','S','Z','a'`)，而非字符串引用，所以 find_string 找不到。

### ❌ 弯路 2: 试图用 LZMA 解压 VSZa

花了大量时间穷举 LZMA 参数偏移（props_off=4..20, data_off 各种组合），全部失败。原因：
- VSZa 使用的**不是 LZMA 压缩算法**
- 而是 Steam 自定义算法（位于 `sub_138E86360`，内部调用 `sub_138E86D20` 实现自定义 bitstream 解压）
- 该算法涉及 CPUID 检测、自定义上下文结构体（约 5KB 大小）

### ❌ 弯路 3: 尝试通过 ctypes 调用 DLL 失败

最初试图调用 `Steam_CreateSteamPipe`（导出函数）启动 DLL，结果返回 0，因为需要 Windows TLS 初始化。但实际上不需要走导出函数路径——直接通过 RVA 调用内部解压函数即可。

### ✅ 正确路径

VSZa 解压函数 `sub_138E86360` 签名非常简单：纯 `buffer → buffer` 解压，不需要 CUtlBuffer 或其他 Steam 运行时结构体。直接用 `ctypes.CFUNCTYPE` 封装后即可调用。

---

## 最终成果

### workshop_download.py (v5)

**核心流程**:
1. SOCKS5 代理 + Steam 匿名登录
2. CDNClient → 内容服务器发现  
3. 获取 manifest → 遍历文件
4. 对每个 chunk: `CDN HTTP 下载 → AES 解密 → 格式识别 → 解压`
5. 支持格式: VSZa (ctypes)、VZa (纯 LZMA)、gzip、ZIP

**依赖**: `ValvePython/steam` + `pysocks` + `ctypes` (stdlib)

**使用**:
```bash
python workshop_download.py <AppID> <WorkshopID>
python workshop_download.py 294100 3683834622
```

**验证结果**: 5/5 文件全部 CRC 校验通过，BatteryIndicator.dll 从原本错误的 10273 B 恢复到正确的 14848 B。

---

## 附录: VSZa 压缩函数 sub_138CECC90 伪代码

```c
int CompressVSZa(void* context, uint8_t* data, int len, CUtlBuffer* out) {
    uint32_t crc = crc32(data, len);
    int compressed_size = SteamCustomCompress(data, len, out->data + out->pos, ...);
    
    out->Write("VSZa", 4);         // magic
    out->Write(crc);               // CRC of original
    // [compressed data goes here - written by compression function]
    out->Write(crc);               // CRC again
    out->Write(len & 0xFFFFFFFF);  // size low
    out->Write(len >> 32);         // size high (0 for <4GB)
    out->Write("zsv", 3);          // footer
    return 1;  // success
}
```

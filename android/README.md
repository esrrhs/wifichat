# WiFiChat Android

Phone client for [WiFiChat](../README.md): anonymous nearby chat & file transfer by modulating Wi-Fi SoftAP SSIDs (no association, no IP, no servers).

## Architecture

```text
Compose UI  →  WifiChatService (FGS)
                    │
                    ▼
              mobile.Session (gomobile / Go engine)
                    │
                    ▼
              AndroidWifiHost → PrivilegeShell
                 ├─ TX/RX preferred: embedded ADB runtime (wireless debugging pair)
                 ├─ TX/RX fallback: external Shizuku user service
                 └─ RX-only fallback: WifiManager scan
```

Stock Android apps cannot change SoftAP SSIDs. WiFiChat embeds an **ADB wireless-debugging runtime** (derived from [android-wireless-debug](https://github.com/xswl369/android-wireless-debug), Apache-2.0): pair once with the system 6-digit code, then the app runs `cmd wifi start-softap` / `list-scan-results` as shell. **No separate Shizuku install is required.** External Shizuku remains an optional fallback.

Without activation the app still runs in **RX-only** mode.

## Activate SoftAP TX (first time)

1. Enable **Developer options → Wireless debugging**.
2. In WiFiChat: Settings → **Activate SoftAP TX**.
3. On the phone open “Pair device with pairing code”, enter the **port + 6-digit code** into WiFiChat.
4. Keep Wireless debugging ON while chatting (reboot usually needs reconnect / re-pair).

## Build

```bash
# From repo root
make android-apk
```

Or:

```bash
bash android/scripts/build_mobile.sh   # → app/libs/wifichat.aar
cd android && ./gradlew :app:assembleDebug
```

Requirements: JDK 17, Android SDK + NDK, Go 1.26+, `gomobile`.

## Modules

| Path | Role |
|------|------|
| `app/` | Compose UI, FGS, privilege orchestration |
| `adb-runtime/` | SPAKE2 pair + STLS ADB shell (vendored / adapted) |
| `../mobile/` | gomobile bind API around the Go engine |

## Notes

* Phone SoftAP is single-slot (`ascii91`); throughput matches the “mobile hotspot” rows in the main README bench table.
* Incoming files land under the app private `files/downloads/` directory.
* `adb-runtime` ships a WiFiChat-specific ADB client identity under `assets/adb/` (not a shared third-party key).

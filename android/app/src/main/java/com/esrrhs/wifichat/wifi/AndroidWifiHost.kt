package com.esrrhs.wifichat.wifi

import android.annotation.SuppressLint
import android.content.Context
import android.net.wifi.WifiManager
import android.os.Build
import android.util.Log
import wifichat.wifichat.Host
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.random.Random

data class ScanBeacon(
    val ssid: String,
    val bssid: String,
    val signal: Int,
    val channel: Int,
)

/**
 * Bridges the Go engine Host interface to Android Wi-Fi APIs.
 *
 * TX (SetSSID): privileged shell via embedded ADB runtime (preferred) or Shizuku.
 * RX (ScanJSON): privileged `cmd wifi list-scan-results`, else WifiManager fallback.
 */
class AndroidWifiHost(
    context: Context,
) : Host {
    private val appContext = context.applicationContext
    private val wifiManager =
        appContext.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager

    private val softApPass = "wc" + Random.nextBytes(5).joinToString("") { "%02x".format(it) }
    private val softApUp = AtomicBoolean(false)
    private var lastSsid: String = ""

    fun canTransmit(): Boolean = PrivilegeShell.isReady()

    override fun setSSID(ssid: String?) {
        val value = ssid.orEmpty()
        if (value.isEmpty()) {
            if (softApUp.get() && PrivilegeShell.isReady()) {
                PrivilegeShell.exec(WifiShellUserService.argsStopSoftAp())
                softApUp.set(false)
            }
            lastSsid = ""
            return
        }
        if (!PrivilegeShell.ensureReady()) {
            throw IllegalStateException(
                "SoftAP TX needs embedded wireless-debug pairing (or Shizuku). Open Settings → Activate.",
            )
        }
        if (value == lastSsid && softApUp.get()) {
            return
        }
        PrivilegeShell.exec(WifiShellUserService.argsStopSoftAp())
        val out = PrivilegeShell.exec(WifiShellUserService.argsForSoftAp(value, softApPass))
        if (out.startsWith("ERR")) {
            throw IllegalStateException("start-softap failed: $out")
        }
        softApUp.set(true)
        lastSsid = value
        Log.d(TAG, "SoftAP SSID updated (${value.length} bytes) via ${PrivilegeShell.backend()}")
    }

    override fun scanJSON(): String {
        if (PrivilegeShell.ensureReady()) {
            PrivilegeShell.exec(WifiShellUserService.argsStartScan())
            val text = PrivilegeShell.exec(WifiShellUserService.argsListScan())
            if (!text.startsWith("ERR")) {
                val parsed = parseCmdScan(text)
                if (parsed.isNotEmpty()) return beaconsToJson(parsed)
            }
        }
        return beaconsToJson(scanViaWifiManager())
    }

    override fun rotateMAC() {
        if (PrivilegeShell.isReady() && softApUp.get()) {
            PrivilegeShell.exec(WifiShellUserService.argsStopSoftAp())
            softApUp.set(false)
            lastSsid = ""
        }
    }

    @SuppressLint("MissingPermission")
    private fun scanViaWifiManager(): List<ScanBeacon> {
        try {
            @Suppress("DEPRECATION")
            wifiManager.startScan()
        } catch (t: Throwable) {
            Log.w(TAG, "WifiManager.startScan failed", t)
        }
        val results = try {
            wifiManager.scanResults
        } catch (t: Throwable) {
            Log.w(TAG, "getScanResults failed", t)
            emptyList()
        }
        return results.mapNotNull { r ->
            val ssid = if (Build.VERSION.SDK_INT >= 33) {
                r.wifiSsid?.toString()?.trim('"') ?: ""
            } else {
                @Suppress("DEPRECATION")
                r.SSID?.trim('"') ?: ""
            }
            if (ssid.isEmpty() || ssid == "<unknown ssid>") return@mapNotNull null
            ScanBeacon(
                ssid = ssid,
                bssid = r.BSSID.orEmpty(),
                signal = r.level,
                channel = freqToChannel(r.frequency),
            )
        }
    }

    companion object {
        private const val TAG = "AndroidWifiHost"

        fun parseCmdScan(text: String): List<ScanBeacon> {
            val out = ArrayList<ScanBeacon>()
            for (line in text.lineSequence()) {
                val fields = line.trim().split(Regex("\\s+"))
                if (fields.size < 5 || !fields[0].contains(':')) continue
                if (fields[0].equals("BSSID", ignoreCase = true)) continue
                val freq = fields[1].toIntOrNull() ?: continue
                val rssi = fields[2].toIntOrNull() ?: continue
                val ssid = fields[4]
                if (ssid.isEmpty()) continue
                out.add(
                    ScanBeacon(
                        ssid = ssid,
                        bssid = fields[0],
                        signal = rssi,
                        channel = freqToChannel(freq),
                    ),
                )
            }
            return out
        }

        fun beaconsToJson(beacons: List<ScanBeacon>): String {
            if (beacons.isEmpty()) return "[]"
            return buildString {
                append('[')
                beacons.forEachIndexed { i, b ->
                    if (i > 0) append(',')
                    append('{')
                    append("\"ssid\":").append(jsonString(b.ssid)).append(',')
                    append("\"bssid\":").append(jsonString(b.bssid)).append(',')
                    append("\"signal\":").append(b.signal).append(',')
                    append("\"channel\":").append(b.channel)
                    append('}')
                }
                append(']')
            }
        }

        private fun jsonString(s: String): String {
            val escaped = s
                .replace("\\", "\\\\")
                .replace("\"", "\\\"")
                .replace("\n", "\\n")
                .replace("\r", "\\r")
                .replace("\t", "\\t")
            return "\"$escaped\""
        }

        fun freqToChannel(freq: Int): Int = when {
            freq == 2484 -> 14
            freq in 2412..2472 -> (freq - 2407) / 5
            freq in 5160..5885 -> (freq - 5000) / 5
            else -> 0
        }
    }
}

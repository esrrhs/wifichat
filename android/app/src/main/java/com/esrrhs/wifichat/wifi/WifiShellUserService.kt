package com.esrrhs.wifichat.wifi

import android.content.Context
import android.util.Log
import androidx.annotation.Keep
import com.esrrhs.wifichat.IWifiShellService
import java.io.BufferedReader
import java.io.InputStreamReader

/**
 * Runs as a Shizuku user service under the shell UID so `cmd wifi` SoftAP /
 * scan commands work without a full root grant.
 */
class WifiShellUserService : IWifiShellService.Stub() {

    override fun exec(argv: Array<String>): String {
        if (argv.isEmpty()) return "ERR: empty argv"
        return try {
            val pb = ProcessBuilder(*argv).redirectErrorStream(true)
            val process = pb.start()
            val out = BufferedReader(InputStreamReader(process.inputStream)).readText()
            val code = process.waitFor()
            if (code == 0) out else "ERR($code): $out"
        } catch (t: Throwable) {
            Log.e(TAG, "exec failed: ${argv.joinToString(" ")}", t)
            "ERR: ${t.message}"
        }
    }

    companion object {
        private const val TAG = "WifiShellUserService"

        @Keep
        @JvmStatic
        fun create(context: Context): Any = WifiShellUserService()

        fun argsForSoftAp(ssid: String, passphrase: String): Array<String> =
            arrayOf("cmd", "wifi", "start-softap", ssid, "wpa2", passphrase, "-b", "2")

        fun argsStopSoftAp(): Array<String> = arrayOf("cmd", "wifi", "stop-softap")

        fun argsStartScan(): Array<String> = arrayOf("cmd", "wifi", "start-scan")

        fun argsListScan(): Array<String> = arrayOf("cmd", "wifi", "list-scan-results")
    }
}

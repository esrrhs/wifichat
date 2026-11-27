package com.esrrhs.wifichat.wifi

import android.content.Context
import android.content.Intent
import android.provider.Settings
import android.util.Log
import com.wirelessdebug.PairState
import com.wirelessdebug.service.AdbKeyStore
import com.wirelessdebug.service.AdbPairClient
import com.wirelessdebug.service.AdbShellController
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * Guides / performs one-shot wireless-debugging pairing for the embedded ADB runtime.
 */
object AdbActivator {
    private const val TAG = "AdbActivator"

    data class Status(
        val paired: Boolean,
        val connected: Boolean,
        val authRejected: Boolean,
        val backend: PrivilegeShell.Backend,
    )

    fun status(context: Context): Status {
        AdbKeyStore.ensureAssets(context)
        return Status(
            paired = PairState.isPaired(context),
            connected = AdbShellController.isConnected(),
            authRejected = AdbShellController.isAuthRejected(),
            backend = PrivilegeShell.backend(),
        )
    }

    fun openWirelessDebuggingSettings(context: Context) {
        val intents = listOf(
            Intent("android.settings.APPLICATION_DEVELOPMENT_SETTINGS"),
            Intent(Settings.ACTION_APPLICATION_DEVELOPMENT_SETTINGS),
            Intent(Settings.ACTION_SETTINGS),
        )
        for (i in intents) {
            try {
                i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                context.startActivity(i)
                return
            } catch (_: Throwable) {
            }
        }
    }

    /** Discover pairing port advertised while the system pairing sheet is open. */
    suspend fun discoverPairPort(context: Context): Int = withContext(Dispatchers.IO) {
        AdbKeyStore.ensureAssets(context)
        AdbShellController.findPairPort(context)
    }

    /**
     * Pair with the 6-digit code shown by the system wireless-debugging UI.
     * [host] defaults to loopback; [pairPort] comes from the pairing sheet / mDNS.
     */
    suspend fun pair(
        context: Context,
        pairPort: Int,
        code: String,
        host: String = "127.0.0.1",
    ): Result<String> = withContext(Dispatchers.IO) {
        AdbKeyStore.ensureAssets(context)
        if (pairPort <= 0) return@withContext Result.failure(IllegalArgumentException("invalid pair port"))
        val r = AdbPairClient.pair(host, pairPort, code.trim())
        if (!r.ok) {
            Log.w(TAG, "pair failed: ${r.message}")
            return@withContext Result.failure(IllegalStateException(r.message ?: "pair failed"))
        }
        PairState.markPaired(context)
        // Immediately open a shell session so SoftAP commands work.
        val connected = AdbShellController.ensureConnected()
        if (!connected) {
            return@withContext Result.success("Paired, but shell connect pending — keep Wireless debugging ON")
        }
        Result.success("Paired and shell ready")
    }

    suspend fun reconnect(context: Context): Boolean = withContext(Dispatchers.IO) {
        AdbKeyStore.ensureAssets(context)
        AdbShellController.ensureConnected()
    }
}

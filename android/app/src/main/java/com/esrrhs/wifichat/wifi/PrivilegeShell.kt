package com.esrrhs.wifichat.wifi

import android.util.Log
import com.wirelessdebug.PairState
import com.wirelessdebug.WdbContext
import com.wirelessdebug.service.AdbShellController

/**
 * Executes privileged shell commands.
 *
 * Preference order:
 * 1. Embedded wireless-debug ADB shell (in-app paired, no external Shizuku)
 * 2. External Shizuku user service (if the user already has Shizuku)
 */
object PrivilegeShell {
    private const val TAG = "PrivilegeShell"

    enum class Backend { ADB, SHIZUKU, NONE }

    fun backend(): Backend = when {
        AdbShellController.isConnected() -> Backend.ADB
        ShizukuShell.isReady() -> Backend.SHIZUKU
        else -> Backend.NONE
    }

    fun isReady(): Boolean = backend() != Backend.NONE

    fun ensureReady(): Boolean {
        if (AdbShellController.isConnected()) return true
        if (tryAdbConnect()) return true
        if (ShizukuShell.isReady()) return true
        return false
    }

    fun exec(argv: Array<String>): String {
        val cmd = argv.joinToString(" ") { quote(it) }
        if (AdbShellController.isConnected() || tryAdbConnect()) {
            val r = AdbShellController.exec(cmd)
            Log.d(TAG, "adb exec: $cmd -> ${r.output.take(120)}")
            return if (r.ok) r.output else "ERR: ${r.output}"
        }
        if (ShizukuShell.isReady()) {
            return ShizukuShell.exec(argv)
        }
        return "ERR: no privileged shell (pair wireless debugging or authorize Shizuku)"
    }

    private fun tryAdbConnect(): Boolean {
        val ctx = WdbContext.get()
        if (ctx != null && !PairState.isPaired(ctx)) {
            return false
        }
        return try {
            AdbShellController.ensureConnected()
        } catch (t: Throwable) {
            Log.d(TAG, "adb ensureConnected failed", t)
            false
        }
    }

    private fun quote(s: String): String {
        if (s.isEmpty()) return "''"
        if (s.all { it.isLetterOrDigit() || it in "-_./:@" }) return s
        return "'" + s.replace("'", "'\\''") + "'"
    }
}

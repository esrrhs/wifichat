package com.esrrhs.wifichat.wifi

import android.content.ComponentName
import android.content.ServiceConnection
import android.content.pm.PackageManager
import android.os.IBinder
import android.util.Log
import com.esrrhs.wifichat.IWifiShellService
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeoutOrNull
import rikka.shizuku.Shizuku
import java.util.concurrent.atomic.AtomicReference

/**
 * Lazily binds a Shizuku user service that can run `cmd wifi` with shell privileges.
 */
object ShizukuShell {
    private const val TAG = "ShizukuShell"
    private val serviceRef = AtomicReference<IWifiShellService?>(null)
    private val bindMutex = Mutex()
    private var connecting: CompletableDeferred<IWifiShellService?>? = null

    private val args = Shizuku.UserServiceArgs(
        ComponentName(
            "com.esrrhs.wifichat",
            WifiShellUserService::class.java.name,
        ),
    )
        .daemon(false)
        .processNameSuffix("shell")
        .debuggable(false)
        .version(1)

    private val connection = object : ServiceConnection {
        override fun onServiceConnected(name: ComponentName?, binder: IBinder?) {
            val svc = IWifiShellService.Stub.asInterface(binder)
            serviceRef.set(svc)
            connecting?.complete(svc)
            connecting = null
            Log.i(TAG, "user service connected")
        }

        override fun onServiceDisconnected(name: ComponentName?) {
            serviceRef.set(null)
            Log.w(TAG, "user service disconnected")
        }
    }

    fun isReady(): Boolean {
        return try {
            Shizuku.pingBinder() &&
                Shizuku.checkSelfPermission() == PackageManager.PERMISSION_GRANTED &&
                serviceRef.get() != null
        } catch (_: Throwable) {
            false
        }
    }

    fun hasPermission(): Boolean {
        return try {
            Shizuku.pingBinder() &&
                Shizuku.checkSelfPermission() == PackageManager.PERMISSION_GRANTED
        } catch (_: Throwable) {
            false
        }
    }

    fun requestPermission(requestCode: Int = 1001) {
        if (!Shizuku.pingBinder()) return
        if (Shizuku.checkSelfPermission() != PackageManager.PERMISSION_GRANTED) {
            Shizuku.requestPermission(requestCode)
        }
    }

    suspend fun ensureBound(timeoutMs: Long = 8_000): Boolean {
        if (serviceRef.get() != null) return true
        if (!hasPermission()) return false
        return bindMutex.withLock {
            serviceRef.get()?.let { return@withLock true }
            val deferred = CompletableDeferred<IWifiShellService?>()
            connecting = deferred
            try {
                Shizuku.bindUserService(args, connection)
            } catch (t: Throwable) {
                Log.e(TAG, "bindUserService failed", t)
                connecting = null
                return@withLock false
            }
            val svc = withTimeoutOrNull(timeoutMs) { deferred.await() }
            svc != null
        }
    }

    fun exec(argv: Array<String>): String {
        val svc = serviceRef.get() ?: return "ERR: shell service not bound"
        return try {
            svc.exec(argv) ?: ""
        } catch (t: Throwable) {
            Log.e(TAG, "remote exec failed", t)
            "ERR: ${t.message}"
        }
    }

    fun unbind() {
        try {
            Shizuku.unbindUserService(args, connection, true)
        } catch (_: Throwable) {
        }
        serviceRef.set(null)
    }
}

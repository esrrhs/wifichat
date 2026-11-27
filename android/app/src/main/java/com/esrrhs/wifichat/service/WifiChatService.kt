package com.esrrhs.wifichat.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.util.Log
import androidx.core.app.NotificationCompat
import com.esrrhs.wifichat.MainActivity
import com.esrrhs.wifichat.R
import com.esrrhs.wifichat.wifi.AndroidWifiHost
import com.esrrhs.wifichat.wifi.PrivilegeShell
import com.esrrhs.wifichat.wifi.ShizukuShell
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import wifichat.wifichat.EventListener
import wifichat.wifichat.Session
import wifichat.wifichat.SessionConfig
import java.io.File
import java.util.concurrent.atomic.AtomicReference

class WifiChatService : Service() {

    data class ChatLine(
        val id: Long,
        val kind: Kind,
        val nick: String = "",
        val peerId: String = "",
        val text: String = "",
        val direct: Boolean = false,
        val micro: Boolean = false,
        val path: String = "",
        val size: Int = 0,
        val timeMs: Long = System.currentTimeMillis(),
    ) {
        enum class Kind { CHAT, FILE, SYSTEM, PROGRESS }
    }

    data class PeerRow(
        val nick: String,
        val peerId: String,
        val sas: String,
        val signal: Int,
    )

    data class LinkState(
        val running: Boolean = false,
        val identityNick: String = "",
        val identityPeerId: String = "",
        val canTransmit: Boolean = false,
        val rxOnly: Boolean = false,
        val stealth: Boolean = false,
        val openChannel: Boolean = true,
        val peerCount: Int = 0,
        val txFrames: Long = 0,
        val rxFrames: Long = 0,
        val ssids: List<String> = emptyList(),
        val statusText: String = "Idle",
        val sending: Boolean = false,
    )

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val hostRef = AtomicReference<AndroidWifiHost?>(null)
    private val sessionRef = AtomicReference<Session?>(null)

    private val _state = MutableStateFlow(LinkState())
    val state: StateFlow<LinkState> = _state.asStateFlow()

    private val _messages = MutableStateFlow<List<ChatLine>>(emptyList())
    val messages: StateFlow<List<ChatLine>> = _messages.asStateFlow()

    private val _peers = MutableStateFlow<List<PeerRow>>(emptyList())
    val peers: StateFlow<List<PeerRow>> = _peers.asStateFlow()

    private val _toast = MutableSharedFlow<String>(extraBufferCapacity = 8)
    val toast: SharedFlow<String> = _toast.asSharedFlow()

    private var lineSeq = 0L

    private val listener = object : EventListener {
        override fun onChat(
            nick: String?,
            peerID: String?,
            text: String?,
            direct: Boolean,
            micro: Boolean,
            unixMs: Long,
        ) {
            append(
                ChatLine(
                    id = nextId(),
                    kind = ChatLine.Kind.CHAT,
                    nick = nick.orEmpty(),
                    peerId = peerID.orEmpty(),
                    text = text.orEmpty(),
                    direct = direct,
                    micro = micro,
                    timeMs = if (unixMs > 0) unixMs else System.currentTimeMillis(),
                ),
            )
            refreshStatus()
        }

        override fun onFile(
            nick: String?,
            peerID: String?,
            fileName: String?,
            savedPath: String?,
            sha256: String?,
            size: Long,
            direct: Boolean,
        ) {
            append(
                ChatLine(
                    id = nextId(),
                    kind = ChatLine.Kind.FILE,
                    nick = nick.orEmpty(),
                    peerId = peerID.orEmpty(),
                    text = fileName.orEmpty(),
                    path = savedPath.orEmpty(),
                    size = size.toInt(),
                    direct = direct,
                ),
            )
            refreshStatus()
        }

        override fun onPeer(nick: String?, peerID: String?, sas: String?, signal: Long) {
            refreshPeers()
            refreshStatus()
        }

        override fun onProgress(
            direction: String?,
            label: String?,
            doneGens: Long,
            totalGens: Long,
            bytes: Long,
            bps: Double,
            complete: Boolean,
        ) {
            val msg = "%s %s  gen %d/%d  %.0f B/s".format(
                direction.orEmpty().uppercase(),
                label.orEmpty(),
                doneGens,
                totalGens,
                bps,
            )
            append(
                ChatLine(
                    id = nextId(),
                    kind = ChatLine.Kind.PROGRESS,
                    text = msg,
                ),
                coalesceProgress = true,
            )
        }

        override fun onEvent(msg: String?) {
            append(
                ChatLine(
                    id = nextId(),
                    kind = ChatLine.Kind.SYSTEM,
                    text = msg.orEmpty(),
                ),
            )
            refreshStatus()
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        instance = this
        createChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                scope.launch { stopLink() }
                stopSelf()
                return START_NOT_STICKY
            }
            ACTION_START -> {
                val nick = intent.getStringExtra(EXTRA_NICK).orEmpty()
                val key = intent.getStringExtra(EXTRA_KEY).orEmpty()
                val stealth = intent.getBooleanExtra(EXTRA_STEALTH, false)
                var rxOnly = intent.getBooleanExtra(EXTRA_RX_ONLY, false)
                val prefix = intent.getStringExtra(EXTRA_PREFIX).orEmpty()
                startForegroundNotification()
                scope.launch {
                    startLink(nick, key, stealth, rxOnly, prefix)
                }
            }
        }
        return START_STICKY
    }

    override fun onDestroy() {
        runCatching { sessionRef.getAndSet(null)?.stop() }
        ShizukuShell.unbind()
        instance = null
        scope.cancel()
        super.onDestroy()
    }

    private suspend fun startLink(
        nick: String,
        key: String,
        stealth: Boolean,
        rxOnlyIn: Boolean,
        prefix: String,
    ) {
        stopLink()
        // Prefer embedded ADB; fall back to external Shizuku if present.
        val adbReady = withContext(Dispatchers.IO) { PrivilegeShell.ensureReady() }
        if (!adbReady) {
            runCatching { ShizukuShell.ensureBound() }
        }
        val host = AndroidWifiHost(this)
        hostRef.set(host)
        var rxOnly = rxOnlyIn
        val backend = PrivilegeShell.backend()
        if (!rxOnly && backend == PrivilegeShell.Backend.NONE) {
            rxOnly = true
            append(
                ChatLine(
                    id = nextId(),
                    kind = ChatLine.Kind.SYSTEM,
                    text = "No privileged shell — RX-only. Open Settings → Activate SoftAP TX (wireless debugging pair), or authorize Shizuku.",
                ),
            )
        } else if (backend != PrivilegeShell.Backend.NONE) {
            append(
                ChatLine(
                    id = nextId(),
                    kind = ChatLine.Kind.SYSTEM,
                    text = when (backend) {
                        PrivilegeShell.Backend.ADB -> "Embedded ADB shell ready — SoftAP TX + privileged scan enabled."
                        PrivilegeShell.Backend.SHIZUKU -> "Shizuku shell ready — SoftAP TX + privileged scan enabled."
                        PrivilegeShell.Backend.NONE -> ""
                    },
                ),
            )
        }

        val downloadDir = File(filesDir, "downloads").apply { mkdirs() }.absolutePath
        val cfg = SessionConfig().also {
            it.setNick(nick)
            it.setKey(key)
            it.setStealth(stealth)
            it.setRXOnly(rxOnly)
            it.setPrefix(prefix)
            it.setDownloadDir(downloadDir)
        }

        val session = withContext(Dispatchers.Default) {
            Session(host, cfg, listener)
        }
        sessionRef.set(session)
        withContext(Dispatchers.Default) { session.start() }

        val idJson = JSONObject(session.identityJSON())
        _state.value = _state.value.copy(
            running = true,
            identityNick = idJson.optString("nick"),
            identityPeerId = idJson.optString("peerId"),
            canTransmit = host.canTransmit() && !rxOnly,
            rxOnly = rxOnly,
            stealth = stealth,
            openChannel = key.isBlank(),
            statusText = if (rxOnly) "Listening (RX-only)" else "On air",
        )
        refreshStatus()
        refreshPeers()
    }

    private suspend fun stopLink() {
        withContext(Dispatchers.Default) {
            sessionRef.getAndSet(null)?.stop()
        }
        hostRef.set(null)
        _state.value = LinkState(statusText = "Idle")
    }

    fun sendText(raw: String) {
        val text = raw.trim()
        if (text.isEmpty()) return
        val session = sessionRef.get() ?: run {
            scope.launch { _toast.emit("Start the link first") }
            return
        }
        scope.launch {
            _state.value = _state.value.copy(sending = true)
            try {
                withContext(Dispatchers.IO) {
                    if (text.startsWith("@")) {
                        val sp = text.indexOf(' ')
                        if (sp <= 1) throw IllegalArgumentException("Usage: @peer message")
                        val peer = text.substring(1, sp).trim()
                        val body = text.substring(sp + 1).trim()
                        session.sendDirect(peer, body)
                    } else {
                        session.sendText(text)
                    }
                }
                append(
                    ChatLine(
                        id = nextId(),
                        kind = ChatLine.Kind.CHAT,
                        nick = "me",
                        peerId = _state.value.identityPeerId,
                        text = text,
                        direct = text.startsWith("@"),
                    ),
                )
            } catch (t: Throwable) {
                Log.e(TAG, "send failed", t)
                append(
                    ChatLine(
                        id = nextId(),
                        kind = ChatLine.Kind.SYSTEM,
                        text = "Send failed: ${t.message}",
                    ),
                )
            } finally {
                _state.value = _state.value.copy(sending = false)
                refreshStatus()
            }
        }
    }

    fun sendFile(path: String, peer: String? = null) {
        val session = sessionRef.get() ?: return
        scope.launch {
            _state.value = _state.value.copy(sending = true)
            try {
                withContext(Dispatchers.IO) {
                    if (peer.isNullOrBlank()) session.sendFile(path)
                    else session.sendDirectFile(peer, path)
                }
                append(
                    ChatLine(
                        id = nextId(),
                        kind = ChatLine.Kind.SYSTEM,
                        text = "File queued on air: ${File(path).name}",
                    ),
                )
            } catch (t: Throwable) {
                append(
                    ChatLine(
                        id = nextId(),
                        kind = ChatLine.Kind.SYSTEM,
                        text = "File send failed: ${t.message}",
                    ),
                )
            } finally {
                _state.value = _state.value.copy(sending = false)
                refreshStatus()
            }
        }
    }

    fun rotateIdentity() {
        val session = sessionRef.get() ?: return
        scope.launch {
            try {
                val json = withContext(Dispatchers.Default) { session.rotate() }
                val obj = JSONObject(json)
                _state.value = _state.value.copy(
                    identityNick = obj.optString("nick"),
                    identityPeerId = obj.optString("peerId"),
                )
                append(
                    ChatLine(
                        id = nextId(),
                        kind = ChatLine.Kind.SYSTEM,
                        text = "Rotated identity → ${obj.optString("nick")} (${obj.optString("peerId")})",
                    ),
                )
            } catch (t: Throwable) {
                _toast.emit("Rotate failed: ${t.message}")
            }
        }
    }

    fun setRoomKey(key: String) {
        sessionRef.get()?.setKey(key)
        _state.value = _state.value.copy(openChannel = key.isBlank())
    }

    fun setStealth(on: Boolean) {
        sessionRef.get()?.setStealth(on)
        _state.value = _state.value.copy(stealth = on)
    }

    private fun refreshStatus() {
        val session = sessionRef.get() ?: return
        try {
            val obj = JSONObject(session.statusJSON())
            val ssidArr = obj.optJSONArray("ssids")
            val ssids = buildList {
                if (ssidArr != null) {
                    for (i in 0 until ssidArr.length()) add(ssidArr.optString(i))
                }
            }
            _state.value = _state.value.copy(
                peerCount = obj.optInt("peers"),
                txFrames = obj.optLong("txFrames"),
                rxFrames = obj.optLong("rxFrames"),
                ssids = ssids,
                openChannel = obj.optBoolean("open"),
                identityNick = obj.optString("nick", _state.value.identityNick),
                identityPeerId = obj.optString("peerId", _state.value.identityPeerId),
            )
        } catch (t: Throwable) {
            Log.w(TAG, "status parse failed", t)
        }
    }

    private fun refreshPeers() {
        val session = sessionRef.get() ?: return
        try {
            val arr = org.json.JSONArray(session.listPeersJSON())
            val rows = buildList {
                for (i in 0 until arr.length()) {
                    val o = arr.getJSONObject(i)
                    add(
                        PeerRow(
                            nick = o.optString("nick"),
                            peerId = o.optString("peerId"),
                            sas = o.optString("sas"),
                            signal = o.optInt("signal"),
                        ),
                    )
                }
            }
            _peers.value = rows
        } catch (t: Throwable) {
            Log.w(TAG, "peers parse failed", t)
        }
    }

    private fun append(line: ChatLine, coalesceProgress: Boolean = false) {
        val cur = _messages.value.toMutableList()
        if (coalesceProgress && cur.isNotEmpty() && cur.last().kind == ChatLine.Kind.PROGRESS) {
            cur[cur.lastIndex] = line
        } else {
            cur.add(line)
            if (cur.size > 500) {
                cur.removeAt(0)
            }
        }
        _messages.value = cur
    }

    private fun nextId(): Long = synchronized(this) { ++lineSeq }

    private fun createChannel() {
        val mgr = getSystemService(NotificationManager::class.java)
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.notification_channel),
            NotificationManager.IMPORTANCE_LOW,
        )
        mgr.createNotificationChannel(channel)
    }

    private fun startForegroundNotification() {
        val pending = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val notification: Notification = NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle(getString(R.string.notification_title))
            .setContentText(getString(R.string.notification_text))
            .setSmallIcon(R.drawable.ic_stat_wifichat)
            .setContentIntent(pending)
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(
                NOTIF_ID,
                notification,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE,
            )
        } else {
            startForeground(NOTIF_ID, notification)
        }
    }

    companion object {
        private const val TAG = "WifiChatService"
        private const val CHANNEL_ID = "wifichat_air"
        private const val NOTIF_ID = 42

        const val ACTION_START = "com.esrrhs.wifichat.START"
        const val ACTION_STOP = "com.esrrhs.wifichat.STOP"
        const val EXTRA_NICK = "nick"
        const val EXTRA_KEY = "key"
        const val EXTRA_STEALTH = "stealth"
        const val EXTRA_RX_ONLY = "rx_only"
        const val EXTRA_PREFIX = "prefix"

        @Volatile
        var instance: WifiChatService? = null
            private set

        fun start(
            context: Context,
            nick: String,
            key: String,
            stealth: Boolean,
            rxOnly: Boolean,
            prefix: String,
        ) {
            val i = Intent(context, WifiChatService::class.java).apply {
                action = ACTION_START
                putExtra(EXTRA_NICK, nick)
                putExtra(EXTRA_KEY, key)
                putExtra(EXTRA_STEALTH, stealth)
                putExtra(EXTRA_RX_ONLY, rxOnly)
                putExtra(EXTRA_PREFIX, prefix)
            }
            context.startForegroundService(i)
        }

        fun stop(context: Context) {
            val i = Intent(context, WifiChatService::class.java).apply { action = ACTION_STOP }
            context.startService(i)
        }
    }
}

package com.esrrhs.wifichat.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.esrrhs.wifichat.data.ChatPrefs
import com.esrrhs.wifichat.data.UserPrefs
import com.esrrhs.wifichat.service.WifiChatService
import com.esrrhs.wifichat.wifi.AdbActivator
import com.esrrhs.wifichat.wifi.PrivilegeShell
import com.esrrhs.wifichat.wifi.ShizukuShell
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import rikka.shizuku.Shizuku

enum class ChatTab { PUBLIC, ROOM, DM }

class ChatViewModel(app: Application) : AndroidViewModel(app) {
    private val prefs = UserPrefs(app)

    val chatPrefs: StateFlow<ChatPrefs> = prefs.flow.stateIn(
        viewModelScope,
        SharingStarted.WhileSubscribed(5_000),
        ChatPrefs(),
    )

    private val _link = MutableStateFlow(WifiChatService.LinkState())
    val link: StateFlow<WifiChatService.LinkState> = _link.asStateFlow()

    private val _messages = MutableStateFlow<List<WifiChatService.ChatLine>>(emptyList())
    val messages: StateFlow<List<WifiChatService.ChatLine>> = _messages.asStateFlow()

    private val _peers = MutableStateFlow<List<WifiChatService.PeerRow>>(emptyList())
    val peers: StateFlow<List<WifiChatService.PeerRow>> = _peers.asStateFlow()

    private val _privilege = MutableStateFlow(PrivilegeShell.Backend.NONE)
    val privilege: StateFlow<PrivilegeShell.Backend> = _privilege.asStateFlow()

    private val _draft = MutableStateFlow("")
    val draft: StateFlow<String> = _draft.asStateFlow()

    private val _activeTab = MutableStateFlow(ChatTab.PUBLIC)
    val activeTab: StateFlow<ChatTab> = _activeTab.asStateFlow()

    private val _dmPeerId = MutableStateFlow<String?>(null)
    val dmPeerId: StateFlow<String?> = _dmPeerId.asStateFlow()

    private val _dmPeerNick = MutableStateFlow<String?>(null)
    val dmPeerNick: StateFlow<String?> = _dmPeerNick.asStateFlow()

    private var collectJobs: List<Job> = emptyList()

    private val permissionResultListener = Shizuku.OnRequestPermissionResultListener { _, grantResult ->
        refreshPrivilege()
        if (grantResult == android.content.pm.PackageManager.PERMISSION_GRANTED) {
            viewModelScope.launch { ShizukuShell.ensureBound() }
        }
    }

    private val binderReceived = Shizuku.OnBinderReceivedListener { refreshPrivilege() }
    private val binderDead = Shizuku.OnBinderDeadListener { refreshPrivilege() }

    init {
        runCatching {
            Shizuku.addRequestPermissionResultListener(permissionResultListener)
            Shizuku.addBinderReceivedListenerSticky(binderReceived)
            Shizuku.addBinderDeadListener(binderDead)
        }
        refreshPrivilege()
        attachIfRunning()
    }

    fun setDraft(value: String) {
        _draft.value = value
    }

    fun savePrefs(transform: (ChatPrefs) -> ChatPrefs) {
        viewModelScope.launch { prefs.update(transform) }
    }

    fun requestShizuku() {
        ShizukuShell.requestPermission()
    }

    fun refreshPrivilege() {
        _privilege.value = PrivilegeShell.backend()
        // Also surface "paired but not connected" via AdbActivator for UI if needed.
        AdbActivator.status(getApplication())
    }

    fun ensureStarted() {
        if (WifiChatService.instance == null) {
            startLink()
        }
    }

    fun loadDemoData() {
        if (_messages.value.isNotEmpty()) return
        val now = System.currentTimeMillis()
        _link.value = _link.value.copy(
            running = true,
            identityNick = "me",
            identityPeerId = "d4e1a902",
            canTransmit = true,
            rxOnly = false,
            openChannel = true,
            peerCount = 3,
            txFrames = 14,
            rxFrames = 38,
            statusText = "On air",
        )
        _peers.value = listOf(
            WifiChatService.PeerRow("alice", "8f2a1104", "7F2-C19", -48),
            WifiChatService.PeerRow("bob", "3c197b22", "4A1-E08", -56),
            WifiChatService.PeerRow("carol", "7a0455e1", "9D3-B12", -64),
        )
        _messages.value = listOf(
            WifiChatService.ChatLine(
                id = 1,
                kind = WifiChatService.ChatLine.Kind.CHAT,
                nick = "alice",
                peerId = "8f2a1104",
                text = "Anyone nearby? No signal in the subway.",
                micro = false,
                isOpenChannel = true,
                timeMs = now - 120_000,
            ),
            WifiChatService.ChatLine(
                id = 2,
                kind = WifiChatService.ChatLine.Kind.CHAT,
                nick = "bob",
                peerId = "3c197b22",
                text = "Copy! At exit B2",
                micro = true,
                isOpenChannel = true,
                timeMs = now - 90_000,
            ),
            WifiChatService.ChatLine(
                id = 3,
                kind = WifiChatService.ChatLine.Kind.CHAT,
                nick = "me",
                peerId = "d4e1a902",
                text = "On my way, 2 mins out!",
                micro = false,
                isOpenChannel = true,
                isMine = true,
                timeMs = now - 60_000,
            ),
            WifiChatService.ChatLine(
                id = 4,
                kind = WifiChatService.ChatLine.Kind.CHAT,
                nick = "carol",
                peerId = "7a0455e1",
                text = "Tap my name if you need the map file over E2EE DM.",
                micro = false,
                isOpenChannel = true,
                timeMs = now - 25_000,
            ),
        )
    }

    fun selectTab(tab: ChatTab) {
        _activeTab.value = tab
        val svc = WifiChatService.instance ?: return
        when (tab) {
            ChatTab.PUBLIC -> svc.setRoomKey("")
            ChatTab.ROOM -> {
                val k = chatPrefs.value.key
                if (k.isNotBlank()) svc.setRoomKey(k)
            }
            ChatTab.DM -> Unit
        }
    }

    fun openDm(peerId: String, nick: String) {
        _dmPeerId.value = peerId
        _dmPeerNick.value = nick
        _activeTab.value = ChatTab.DM
    }

    fun clearDmPeer() {
        _dmPeerId.value = null
        _dmPeerNick.value = null
    }

    fun startLink() {
        val p = chatPrefs.value
        val initialKey = if (_activeTab.value == ChatTab.ROOM) p.key else ""
        WifiChatService.start(
            getApplication(),
            nick = p.nick,
            key = initialKey,
            stealth = p.stealth,
            rxOnly = p.rxOnly,
            prefix = p.prefix,
        )
        viewModelScope.launch {
            repeat(40) {
                val svc = WifiChatService.instance
                if (svc != null) {
                    attach(svc)
                    refreshPrivilege()
                    return@launch
                }
                kotlinx.coroutines.delay(100)
            }
        }
    }

    fun stopLink() {
        WifiChatService.stop(getApplication())
        collectJobs.forEach { it.cancel() }
        collectJobs = emptyList()
        _link.value = WifiChatService.LinkState(statusText = "Idle")
    }

    fun sendDraft() {
        val text = _draft.value
        val target = if (_activeTab.value == ChatTab.DM) _dmPeerId.value else null
        if (_activeTab.value == ChatTab.DM && target.isNullOrBlank()) return
        WifiChatService.instance?.sendText(text, directPeer = target)
        _draft.value = ""
    }

    fun sendFile(path: String, peer: String? = null) {
        val target = peer ?: if (_activeTab.value == ChatTab.DM) _dmPeerId.value else null
        WifiChatService.instance?.sendFile(path, target)
    }

    fun rotate() {
        WifiChatService.instance?.rotateIdentity()
    }

    fun applyNick(nick: String) {
        savePrefs { it.copy(nick = nick) }
        WifiChatService.instance?.setNick(nick)
    }

    fun applyKey(key: String) {
        savePrefs { it.copy(key = key) }
        if (_activeTab.value == ChatTab.ROOM || key.isNotBlank()) {
            WifiChatService.instance?.setRoomKey(key)
        }
    }

    fun joinRoom(key: String) {
        applyKey(key)
    }

    fun applyStealth(on: Boolean) {
        savePrefs { it.copy(stealth = on) }
        WifiChatService.instance?.setStealth(on)
    }

    private fun attachIfRunning() {
        WifiChatService.instance?.let { attach(it) }
    }

    private fun attach(svc: WifiChatService) {
        collectJobs.forEach { it.cancel() }
        collectJobs = listOf(
            viewModelScope.launch { svc.state.collect { _link.value = it } },
            viewModelScope.launch { svc.messages.collect { _messages.value = it } },
            viewModelScope.launch { svc.peers.collect { _peers.value = it } },
        )
    }

    override fun onCleared() {
        runCatching {
            Shizuku.removeRequestPermissionResultListener(permissionResultListener)
            Shizuku.removeBinderReceivedListener(binderReceived)
            Shizuku.removeBinderDeadListener(binderDead)
        }
        super.onCleared()
    }
}

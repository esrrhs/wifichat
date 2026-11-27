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

    fun startLink() {
        val p = chatPrefs.value
        WifiChatService.start(
            getApplication(),
            nick = p.nick,
            key = p.key,
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
        WifiChatService.instance?.sendText(text)
        _draft.value = ""
    }

    fun sendFile(path: String, peer: String? = null) {
        WifiChatService.instance?.sendFile(path, peer)
    }

    fun rotate() {
        WifiChatService.instance?.rotateIdentity()
    }

    fun applyKey(key: String) {
        savePrefs { it.copy(key = key) }
        WifiChatService.instance?.setRoomKey(key)
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

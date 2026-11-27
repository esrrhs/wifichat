package com.esrrhs.wifichat.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.HelpOutline
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.AttachFile
import androidx.compose.material.icons.filled.Groups
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.PowerSettingsNew
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Wifi
import androidx.compose.material.icons.filled.WifiOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SecondaryTabRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.esrrhs.wifichat.service.WifiChatService
import com.esrrhs.wifichat.wifi.PrivilegeShell
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatScreen(
    vm: ChatViewModel,
    onPickFile: () -> Unit,
    onRequestPermissions: () -> Unit,
) {
    val link by vm.link.collectAsStateWithLifecycle()
    val messages by vm.messages.collectAsStateWithLifecycle()
    val peers by vm.peers.collectAsStateWithLifecycle()
    val prefs by vm.chatPrefs.collectAsStateWithLifecycle()
    val draft by vm.draft.collectAsStateWithLifecycle()
    val privilege by vm.privilege.collectAsStateWithLifecycle()
    val activeTab by vm.activeTab.collectAsStateWithLifecycle()
    val dmPeerId by vm.dmPeerId.collectAsStateWithLifecycle()
    val dmPeerNick by vm.dmPeerNick.collectAsStateWithLifecycle()

    var showSettings by remember { mutableStateOf(false) }
    var showPeers by remember { mutableStateOf(false) }
    var showHelp by remember { mutableStateOf(false) }
    var showAdbActivate by remember { mutableStateOf(false) }
    val listState = rememberLazyListState()
    val privilegeReady = privilege != PrivilegeShell.Backend.NONE

    LaunchedEffect(Unit) {
        onRequestPermissions()
        if (messages.isEmpty()) {
            vm.ensureStarted()
        }
    }

    val visibleMessages = remember(messages, activeTab, dmPeerId) {
        when (activeTab) {
            ChatTab.PUBLIC -> messages.filter {
                it.kind == WifiChatService.ChatLine.Kind.SYSTEM ||
                    it.kind == WifiChatService.ChatLine.Kind.PROGRESS ||
                    (!it.direct && it.isOpenChannel)
            }
            ChatTab.ROOM -> messages.filter {
                it.kind == WifiChatService.ChatLine.Kind.SYSTEM ||
                    it.kind == WifiChatService.ChatLine.Kind.PROGRESS ||
                    (!it.direct && !it.isOpenChannel)
            }
            ChatTab.DM -> messages.filter {
                it.direct && (dmPeerId == null || it.targetPeer == dmPeerId || it.peerId == dmPeerId)
            }
        }
    }

    LaunchedEffect(visibleMessages.size) {
        if (visibleMessages.isNotEmpty()) {
            listState.animateScrollToItem(visibleMessages.lastIndex)
        }
    }

    CompositionLocalProvider(LocalContentColor provides MaterialTheme.colorScheme.onBackground) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(
                    Brush.verticalGradient(
                        listOf(
                            Color(0xFF0B1220),
                            Color(0xFF0E1A2E),
                            Color(0xFF0B1220),
                        ),
                    ),
                ),
        ) {
        // Soft radar rings behind the header
        Box(
            Modifier
                .fillMaxWidth()
                .height(220.dp)
                .background(
                    Brush.radialGradient(
                        colors = listOf(Color(0x332BB0ED), Color.Transparent),
                        radius = 520f,
                    ),
                ),
        )

        Column(
            Modifier
                .fillMaxSize()
                .statusBarsPadding()
                .navigationBarsPadding(),
        ) {
            TopBar(
                link = link,
                peerCount = peers.size,
                privilege = privilege,
                onHelp = { showHelp = true },
                onSettings = { showSettings = true },
                onPeers = { showPeers = true },
                onToggle = {
                    if (link.running) vm.stopLink() else {
                        onRequestPermissions()
                        vm.startLink()
                    }
                },
                onRotate = { vm.rotate() },
            )

            SecondaryTabRow(
                selectedTabIndex = activeTab.ordinal,
                containerColor = Color.Transparent,
                contentColor = MaterialTheme.colorScheme.primary,
            ) {
                Tab(
                    selected = activeTab == ChatTab.PUBLIC,
                    onClick = { vm.selectTab(ChatTab.PUBLIC) },
                    text = { Text("Public") },
                )
                Tab(
                    selected = activeTab == ChatTab.ROOM,
                    onClick = { vm.selectTab(ChatTab.ROOM) },
                    text = { Text("Room") },
                )
                Tab(
                    selected = activeTab == ChatTab.DM,
                    onClick = { vm.selectTab(ChatTab.DM) },
                    text = {
                        Text(
                            if (dmPeerNick != null) "DM · $dmPeerNick" else "DM",
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    },
                )
            }

            if (activeTab == ChatTab.ROOM) {
                RoomKeyBar(
                    currentKey = prefs.key,
                    onJoinRoom = { vm.joinRoom(it) },
                )
            } else if (activeTab == ChatTab.DM) {
                DmHeaderBar(
                    dmPeerId = dmPeerId,
                    dmPeerNick = dmPeerNick,
                    peers = peers,
                    onSelectPeer = { p -> vm.openDm(p.peerId, p.nick) },
                    onClearPeer = { vm.clearDmPeer() },
                )
            }

            LazyColumn(
                state = listState,
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth(),
                contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                if (visibleMessages.isEmpty()) {
                    item {
                        EmptyHint(
                            tab = activeTab,
                            running = link.running,
                            roomKeySet = prefs.key.isNotBlank(),
                            dmPeerSelected = dmPeerId != null,
                        )
                    }
                }
                items(visibleMessages, key = { it.id }) { line ->
                    MessageBubble(
                        line = line,
                        onPeerClick = { peerId, nick ->
                            if (peerId.isNotEmpty() && !line.isMine && line.nick != "me") {
                                vm.openDm(peerId, nick)
                            }
                        },
                    )
                }
            }

            if (!privilegeReady || link.rxOnly) {
                TxOnboardingBanner(
                    hasNick = prefs.nick.isNotBlank(),
                    onOpenSettings = { showSettings = true },
                    onActivateTx = { showAdbActivate = true },
                )
            }

            val canSendHere = link.running && !link.sending &&
                (activeTab != ChatTab.ROOM || prefs.key.isNotBlank()) &&
                (activeTab != ChatTab.DM || dmPeerId != null)

            Composer(
                draft = draft,
                enabled = canSendHere,
                sending = link.sending,
                placeholderText = when {
                    link.sending -> "Transmitting over SSIDs…"
                    !link.running -> "Tap Wi-Fi icon above to go on air"
                    activeTab == ChatTab.ROOM && prefs.key.isBlank() -> "Enter a room passphrase above first"
                    activeTab == ChatTab.DM && dmPeerId == null -> "Select a nearby user to start E2EE DM"
                    activeTab == ChatTab.DM -> "E2EE message to $dmPeerNick…"
                    activeTab == ChatTab.ROOM -> "Encrypted room message…"
                    else -> "Message Public chat…"
                },
                onDraft = vm::setDraft,
                onSend = vm::sendDraft,
                onAttach = onPickFile,
            )
        }
    }

    if (showSettings) {
        SettingsSheet(
            prefsNick = prefs.nick,
            prefsKey = prefs.key,
            stealth = prefs.stealth,
            rxOnly = prefs.rxOnly,
            prefix = prefs.prefix,
            privilege = privilege,
            onDismiss = { showSettings = false },
            onSaveNick = { vm.applyNick(it) },
            onSaveKey = { vm.applyKey(it) },
            onStealth = { vm.applyStealth(it) },
            onRxOnly = { vm.savePrefs { p -> p.copy(rxOnly = it) } },
            onPrefix = { vm.savePrefs { p -> p.copy(prefix = it) } },
            onActivateAdb = {
                showSettings = false
                showAdbActivate = true
            },
            onRequestShizuku = { vm.requestShizuku() },
        )
    }

    if (showHelp) {
        HelpSheet(
            onDismiss = { showHelp = false },
            onActivateTx = {
                showHelp = false
                showAdbActivate = true
            },
        )
    }

    if (showAdbActivate) {
        AdbActivationSheet(
            onDismiss = {
                showAdbActivate = false
                vm.refreshPrivilege()
            },
            onActivated = { vm.refreshPrivilege() },
        )
    }

    if (showPeers) {
        PeersDialog(
            peers = peers,
            onDismiss = { showPeers = false },
            onStartDm = { p ->
                showPeers = false
                vm.openDm(p.peerId, p.nick)
            },
        )
    }
    }
}

@Composable
private fun TopBar(
    link: WifiChatService.LinkState,
    peerCount: Int,
    privilege: PrivilegeShell.Backend,
    onHelp: () -> Unit,
    onSettings: () -> Unit,
    onPeers: () -> Unit,
    onToggle: () -> Unit,
    onRotate: () -> Unit,
) {
    Column(Modifier.padding(horizontal = 16.dp, vertical = 10.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "WiFiChat",
                style = MaterialTheme.typography.displayLarge.copy(
                    brush = Brush.linearGradient(
                        listOf(Color(0xFF3DDC97), Color(0xFF2BB0ED)),
                    ),
                ),
                modifier = Modifier.weight(1f),
            )
            IconButton(onClick = onPeers) {
                Icon(Icons.Default.Groups, contentDescription = "Peers")
            }
            IconButton(onClick = onHelp) {
                Icon(Icons.AutoMirrored.Filled.HelpOutline, contentDescription = "Help")
            }
            IconButton(onClick = onSettings) {
                Icon(Icons.Default.Settings, contentDescription = "Settings")
            }
            IconButton(onClick = onToggle) {
                Icon(
                    if (link.running) Icons.Default.PowerSettingsNew else Icons.Default.Wifi,
                    contentDescription = "Toggle link",
                    tint = if (link.running) MaterialTheme.colorScheme.secondary
                    else MaterialTheme.colorScheme.primary,
                )
            }
        }

        Spacer(Modifier.height(6.dp))

        Surface(
            shape = RoundedCornerShape(14.dp),
            color = MaterialTheme.colorScheme.surface.copy(alpha = 0.9f),
            tonalElevation = 0.dp,
        ) {
            Row(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Box(
                    Modifier
                        .size(10.dp)
                        .clip(CircleShape)
                        .background(
                            when {
                                !link.running -> Color(0xFF5B6B86)
                                link.rxOnly -> Color(0xFFF0C14A)
                                else -> MaterialTheme.colorScheme.secondary
                            },
                        ),
                )
                Spacer(Modifier.width(10.dp))
                Column(Modifier.weight(1f)) {
                    Text(
                        text = if (link.running) {
                            "${link.identityNick} · ${link.identityPeerId.ifEmpty { "----" }}"
                        } else {
                            "Link offline"
                        },
                        style = MaterialTheme.typography.titleMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        text = buildString {
                            append(link.statusText)
                            if (link.running) {
                                append(" · peers ")
                                append(peerCount)
                                append(" · tx/rx ")
                                append(link.txFrames)
                                append('/')
                                append(link.rxFrames)
                            }
                            append(
                                when (privilege) {
                                    PrivilegeShell.Backend.ADB -> " · ADB shell"
                                    PrivilegeShell.Backend.SHIZUKU -> " · Shizuku"
                                    PrivilegeShell.Backend.NONE -> " · RX-only"
                                },
                            )
                        },
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                    )
                }
                if (link.running) {
                    IconButton(onClick = onRotate) {
                        Icon(Icons.Default.Refresh, contentDescription = "Rotate identity")
                    }
                }
            }
        }
    }
}

@Composable
private fun RoomKeyBar(
    currentKey: String,
    onJoinRoom: (String) -> Unit,
) {
    var inputKey by remember(currentKey) { mutableStateOf(currentKey) }
    Surface(
        color = MaterialTheme.colorScheme.surface.copy(alpha = 0.65f),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Icon(
                Icons.Default.Lock,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.secondary,
                modifier = Modifier.size(18.dp),
            )
            OutlinedTextField(
                value = inputKey,
                onValueChange = { inputKey = it },
                placeholder = { Text("Enter room passphrase") },
                singleLine = true,
                modifier = Modifier.weight(1f),
            )
            FilledTonalButton(
                onClick = { onJoinRoom(inputKey.trim()) },
                enabled = inputKey.isNotBlank(),
            ) {
                Text("Join")
            }
        }
    }
}

@Composable
private fun DmHeaderBar(
    dmPeerId: String?,
    dmPeerNick: String?,
    peers: List<WifiChatService.PeerRow>,
    onSelectPeer: (WifiChatService.PeerRow) -> Unit,
    onClearPeer: () -> Unit,
) {
    Surface(
        color = MaterialTheme.colorScheme.surface.copy(alpha = 0.65f),
        modifier = Modifier.fillMaxWidth(),
    ) {
        if (dmPeerId != null) {
            Row(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = "E2EE DM · ${dmPeerNick.orEmpty()} (${dmPeerId.take(4)})",
                    style = MaterialTheme.typography.titleSmall,
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.weight(1f),
                )
                TextButton(onClick = onClearPeer) {
                    Text("Switch")
                }
            }
        } else if (peers.isNotEmpty()) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                Text(
                    "Tap a nearby peer to start E2EE DM:",
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                peers.forEach { p ->
                    Surface(
                        shape = RoundedCornerShape(10.dp),
                        color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f),
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable { onSelectPeer(p) },
                    ) {
                        Row(
                            Modifier.padding(horizontal = 12.dp, vertical = 8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Text(
                                "${p.nick} (#${p.peerId.take(4)})",
                                style = MaterialTheme.typography.bodyMedium,
                                fontWeight = FontWeight.Medium,
                                modifier = Modifier.weight(1f),
                            )
                            Text(
                                "SAS ${p.sas}",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.secondary,
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun TxOnboardingBanner(
    hasNick: Boolean,
    onOpenSettings: () -> Unit,
    onActivateTx: () -> Unit,
) {
    Surface(
        color = Color(0xFF13263F),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 12.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(
                text = if (hasNick) {
                    "RX-only · Activate Wireless Debugging to send"
                } else {
                    "RX-only · Set nickname & activate TX to chat"
                },
                style = MaterialTheme.typography.labelMedium,
                color = Color(0xFF7DD3FC),
                modifier = Modifier.weight(1f),
            )
            if (!hasNick) {
                TextButton(onClick = onOpenSettings) {
                    Text("Set Nick")
                }
            }
            FilledTonalButton(onClick = onActivateTx) {
                Text("Activate TX")
            }
        }
    }
}

@Composable
private fun EmptyHint(
    tab: ChatTab,
    running: Boolean,
    roomKeySet: Boolean,
    dmPeerSelected: Boolean,
) {
    Column(
        Modifier
            .fillMaxWidth()
            .padding(top = 42.dp, start = 24.dp, end = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(
            if (running) Icons.Default.Wifi else Icons.Default.WifiOff,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.primary.copy(alpha = 0.7f),
            modifier = Modifier.size(44.dp),
        )
        Spacer(Modifier.height(12.dp))
        Text(
            text = when {
                !running -> "Tap the Wi-Fi button to go on air"
                tab == ChatTab.ROOM && !roomKeySet -> "Enter a room passphrase above to join"
                tab == ChatTab.DM && !dmPeerSelected -> "Tap any user in Public / Room chat or Peers to start an E2EE DM"
                else -> "Listening to nearby Wi-Fi SSIDs…"
            },
            style = MaterialTheme.typography.bodyLarge,
        )
        Spacer(Modifier.height(6.dp))
        Text(
            text = "Tap any sender's nickname to start a private E2EE DM.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun MessageBubble(
    line: WifiChatService.ChatLine,
    onPeerClick: (peerId: String, nick: String) -> Unit,
) {
    val time = remember(line.timeMs) {
        SimpleDateFormat("HH:mm:ss", Locale.getDefault()).format(Date(line.timeMs))
    }
    when (line.kind) {
        WifiChatService.ChatLine.Kind.SYSTEM, WifiChatService.ChatLine.Kind.PROGRESS -> {
            Text(
                text = "· $time  ${line.text}",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
        WifiChatService.ChatLine.Kind.CHAT, WifiChatService.ChatLine.Kind.FILE -> {
            val mine = line.isMine || line.nick == "me"
            val accent = when {
                line.direct -> MaterialTheme.colorScheme.primary
                line.micro -> MaterialTheme.colorScheme.secondary
                else -> MaterialTheme.colorScheme.surfaceVariant
            }
            Column(
                Modifier.fillMaxWidth(),
                horizontalAlignment = if (mine) Alignment.End else Alignment.Start,
            ) {
                Text(
                    text = buildString {
                        append(time)
                        append("  ")
                        append(if (line.direct) "E2EE" else if (line.micro) "1-SSID" else "NEARBY")
                        append("  ")
                        append(line.nick)
                        if (line.peerId.isNotEmpty() && !mine) {
                            append('#')
                            append(line.peerId.take(4))
                        }
                    },
                    style = MaterialTheme.typography.labelSmall,
                    color = if (mine) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.primary,
                    modifier = if (!mine && line.peerId.isNotEmpty()) {
                        Modifier.clickable { onPeerClick(line.peerId, line.nick) }
                    } else {
                        Modifier
                    },
                )
                Surface(
                    shape = RoundedCornerShape(
                        topStart = 14.dp,
                        topEnd = 14.dp,
                        bottomStart = if (mine) 14.dp else 4.dp,
                        bottomEnd = if (mine) 4.dp else 14.dp,
                    ),
                    color = if (mine) MaterialTheme.colorScheme.primary.copy(alpha = 0.18f) else accent.copy(alpha = 0.35f),
                    modifier = if (!mine && line.peerId.isNotEmpty()) {
                        Modifier.clickable { onPeerClick(line.peerId, line.nick) }
                    } else {
                        Modifier
                    },
                ) {
                    Text(
                        text = if (line.kind == WifiChatService.ChatLine.Kind.FILE) {
                            "📎 ${line.text} (${line.size} B)\n${line.path}"
                        } else {
                            line.text
                        },
                        style = MaterialTheme.typography.bodyLarge,
                        modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp),
                    )
                }
            }
        }
    }
}

@Composable
private fun Composer(
    draft: String,
    enabled: Boolean,
    sending: Boolean,
    placeholderText: String,
    onDraft: (String) -> Unit,
    onSend: () -> Unit,
    onAttach: () -> Unit,
) {
    Surface(
        color = MaterialTheme.colorScheme.surface.copy(alpha = 0.95f),
        tonalElevation = 0.dp,
    ) {
        Row(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 12.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            IconButton(onClick = onAttach, enabled = enabled) {
                Icon(Icons.Default.AttachFile, contentDescription = "Send file")
            }
            OutlinedTextField(
                value = draft,
                onValueChange = onDraft,
                modifier = Modifier.weight(1f),
                enabled = enabled,
                placeholder = { Text(placeholderText, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
                keyboardActions = KeyboardActions(onSend = { if (enabled && draft.isNotBlank()) onSend() }),
                maxLines = 4,
            )
            Spacer(Modifier.width(6.dp))
            IconButton(onClick = onSend, enabled = enabled && draft.isNotBlank()) {
                Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Send")
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun HelpSheet(
    onDismiss: () -> Unit,
    onActivateTx: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(
            Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text("How to use WiFiChat", style = MaterialTheme.typography.headlineMedium)

            HelpSection(
                title = "1. Zero-Internet Air Chat",
                body = "WiFiChat transmits encrypted text and files using 32-byte Wi-Fi hotspot names (SSIDs). No internet, SIM card, or router connection is required.",
            )
            HelpSection(
                title = "2. Public, Room & DM Tabs",
                body = "• Public: Open broadcast channel visible to all nearby WiFiChat users.\n" +
                    "• Room: Enter a shared passphrase to chat in an Argon2id-isolated private room.\n" +
                    "• DM: Tap any user's nickname in Public/Room chat (or in Peers) for X25519 end-to-end encrypted direct chat.",
            )
            HelpSection(
                title = "3. Nickname & Sending (SoftAP TX)",
                body = "Passive listening (RX-only) works out of the box. To send messages:\n" +
                    "1) Set your nickname in Settings (otherwise an anonymous anon-xxxx alias is used).\n" +
                    "2) Enable Developer options → Wireless debugging on your phone, then tap Activate SoftAP TX and pair once.",
            )
            HelpSection(
                title = "4. Ephemeral Privacy",
                body = "Keys exist in RAM only. Tap the refresh icon in the header anytime to rotate your X25519 identity, PeerID, and hotspot MAC.",
            )

            FilledTonalButton(
                onClick = onActivateTx,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("Activate SoftAP TX (wireless debugging)")
            }
            Button(
                onClick = onDismiss,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("Got it")
            }
            Spacer(Modifier.height(16.dp))
        }
    }
}

@Composable
private fun HelpSection(title: String, body: String) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(title, style = MaterialTheme.typography.titleMedium, color = MaterialTheme.colorScheme.primary)
        Text(body, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SettingsSheet(
    prefsNick: String,
    prefsKey: String,
    stealth: Boolean,
    rxOnly: Boolean,
    prefix: String,
    privilege: PrivilegeShell.Backend,
    onDismiss: () -> Unit,
    onSaveNick: (String) -> Unit,
    onSaveKey: (String) -> Unit,
    onStealth: (Boolean) -> Unit,
    onRxOnly: (Boolean) -> Unit,
    onPrefix: (String) -> Unit,
    onActivateAdb: () -> Unit,
    onRequestShizuku: () -> Unit,
) {
    var nick by remember(prefsNick) { mutableStateOf(prefsNick) }
    var key by remember(prefsKey) { mutableStateOf(prefsKey) }
    var pref by remember(prefix) { mutableStateOf(prefix) }
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(
            Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text("Air settings", style = MaterialTheme.typography.headlineMedium)
            OutlinedTextField(
                value = nick,
                onValueChange = { nick = it },
                label = { Text("Nickname (optional)") },
                placeholder = { Text("Leave blank for anon-xxxx") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true,
            )
            OutlinedTextField(
                value = key,
                onValueChange = { key = it },
                label = { Text("Room passphrase") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true,
            )
            OutlinedTextField(
                value = pref,
                onValueChange = { pref = it },
                label = { Text("SSID camouflage prefix") },
                placeholder = { Text("e.g. CMCC-") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true,
            )
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text("Stealth idle", Modifier.weight(1f))
                Switch(checked = stealth, onCheckedChange = onStealth)
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text("Force RX-only", Modifier.weight(1f))
                Switch(checked = rxOnly, onCheckedChange = onRxOnly)
            }
            HorizontalDivider()
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("SoftAP privilege")
                Text(
                    when (privilege) {
                        PrivilegeShell.Backend.ADB -> "Embedded ADB shell connected"
                        PrivilegeShell.Backend.SHIZUKU -> "External Shizuku authorized"
                        PrivilegeShell.Backend.NONE -> "Not activated — TX disabled"
                    },
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                FilledTonalButton(
                    onClick = onActivateAdb,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("Activate SoftAP TX (wireless debugging)")
                }
                if (privilege != PrivilegeShell.Backend.SHIZUKU) {
                    TextButton(onClick = onRequestShizuku) {
                        Text("Or authorize external Shizuku")
                    }
                }
            }
            Button(
                onClick = {
                    onSaveNick(nick.trim())
                    onSaveKey(key)
                    onPrefix(pref.trim())
                    onDismiss()
                },
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("Save")
            }
            Spacer(Modifier.height(16.dp))
        }
    }
}

@Composable
private fun PeersDialog(
    peers: List<WifiChatService.PeerRow>,
    onDismiss: () -> Unit,
    onStartDm: (WifiChatService.PeerRow) -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Nearby peers") },
        text = {
            if (peers.isEmpty()) {
                Text("No Hello beacons decoded yet. Wait for nearby nodes or disable stealth.")
            } else {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    peers.forEach { p ->
                        Surface(
                            shape = RoundedCornerShape(8.dp),
                            color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.4f),
                            modifier = Modifier
                                .fillMaxWidth()
                                .clickable { onStartDm(p) },
                        ) {
                            Text(
                                "${p.nick}  #${p.peerId.take(4)}  SAS=${p.sas}  RSSI=${p.signal}",
                                style = MaterialTheme.typography.bodyMedium,
                                modifier = Modifier.padding(10.dp),
                            )
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) { Text("Close") }
        },
    )
}

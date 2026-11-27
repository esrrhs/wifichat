package com.esrrhs.wifichat.ui

import androidx.compose.foundation.background
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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.AttachFile
import androidx.compose.material.icons.filled.Groups
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
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
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

    var showSettings by remember { mutableStateOf(false) }
    var showPeers by remember { mutableStateOf(false) }
    var showAdbActivate by remember { mutableStateOf(false) }
    val listState = rememberLazyListState()
    val privilegeReady = privilege != PrivilegeShell.Backend.NONE

    LaunchedEffect(messages.size) {
        if (messages.isNotEmpty()) {
            listState.animateScrollToItem(messages.lastIndex)
        }
    }

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

            LazyColumn(
                state = listState,
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth(),
                contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                if (messages.isEmpty()) {
                    item {
                        EmptyHint(running = link.running, privilegeReady = privilegeReady)
                    }
                }
                items(messages, key = { it.id }) { line ->
                    MessageBubble(line)
                }
            }

            Composer(
                draft = draft,
                enabled = link.running && !link.sending,
                sending = link.sending,
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
            onSaveNick = { vm.savePrefs { p -> p.copy(nick = it) } },
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
        PeersDialog(peers = peers, onDismiss = { showPeers = false })
    }
}

@Composable
private fun TopBar(
    link: WifiChatService.LinkState,
    peerCount: Int,
    privilege: PrivilegeShell.Backend,
    onSettings: () -> Unit,
    onPeers: () -> Unit,
    onToggle: () -> Unit,
    onRotate: () -> Unit,
) {
    Column(Modifier.padding(horizontal = 16.dp, vertical = 12.dp)) {
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

        Text(
            text = "SSID air-gap chat · zero association",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )

        Spacer(Modifier.height(10.dp))

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
                                    PrivilegeShell.Backend.NONE -> " · TX locked"
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
private fun EmptyHint(running: Boolean, privilegeReady: Boolean) {
    Column(
        Modifier
            .fillMaxWidth()
            .padding(top = 48.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(
            if (running) Icons.Default.Wifi else Icons.Default.WifiOff,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.primary.copy(alpha = 0.7f),
            modifier = Modifier.size(48.dp),
        )
        Spacer(Modifier.height(12.dp))
        Text(
            text = if (running) {
                "Listening to nearby SSIDs…"
            } else {
                "Tap the Wi-Fi button to go on air"
            },
            style = MaterialTheme.typography.bodyLarge,
        )
        Spacer(Modifier.height(6.dp))
        Text(
            text = if (privilegeReady) {
                "SoftAP TX + privileged scan ready"
            } else {
                "Settings → Activate SoftAP TX (wireless debugging pair) for full TX; otherwise RX-only"
            },
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun MessageBubble(line: WifiChatService.ChatLine) {
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
            val mine = line.nick == "me"
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
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Surface(
                    shape = RoundedCornerShape(
                        topStart = 14.dp,
                        topEnd = 14.dp,
                        bottomStart = if (mine) 14.dp else 4.dp,
                        bottomEnd = if (mine) 4.dp else 14.dp,
                    ),
                    color = if (mine) MaterialTheme.colorScheme.primary.copy(alpha = 0.18f) else accent.copy(alpha = 0.35f),
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
                placeholder = {
                    Text(
                        if (sending) "Transmitting over SSIDs…"
                        else "Message  ·  @peer for E2EE",
                    )
                },
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
                keyboardActions = KeyboardActions(onSend = { if (enabled) onSend() }),
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
                .padding(horizontal = 20.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text("Air settings", style = MaterialTheme.typography.headlineMedium)
            OutlinedTextField(
                value = nick,
                onValueChange = { nick = it },
                label = { Text("Alias (optional)") },
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
private fun PeersDialog(peers: List<WifiChatService.PeerRow>, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Nearby peers") },
        text = {
            if (peers.isEmpty()) {
                Text("No Hello beacons decoded yet. Wait for nearby nodes or disable stealth.")
            } else {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    peers.forEach { p ->
                        Text(
                            "${p.nick}  id=${p.peerId}  SAS=${p.sas}  RSSI=${p.signal}",
                            style = MaterialTheme.typography.bodyMedium,
                        )
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) { Text("Close") }
        },
    )
}

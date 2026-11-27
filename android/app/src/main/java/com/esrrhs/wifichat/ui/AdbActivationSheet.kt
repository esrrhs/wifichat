package com.esrrhs.wifichat.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.esrrhs.wifichat.wifi.AdbActivator
import com.esrrhs.wifichat.wifi.PrivilegeShell
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AdbActivationSheet(
    onDismiss: () -> Unit,
    onActivated: () -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    var status by remember { mutableStateOf(AdbActivator.status(context)) }
    var pairPort by remember { mutableStateOf("") }
    var pairCode by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var message by remember { mutableStateOf("") }

    LaunchedEffect(Unit) {
        status = AdbActivator.status(context)
        if (!status.connected) {
            val port = AdbActivator.discoverPairPort(context)
            if (port > 0) pairPort = port.toString()
        }
    }

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 20.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text("Activate SoftAP TX", style = MaterialTheme.typography.headlineMedium)
            Text(
                "Android blocks normal apps from renaming the hotspot. " +
                    "WiFiChat embeds an ADB runtime: pair once with Wireless debugging " +
                    "(developer options). No separate Shizuku install required.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            Text(
                buildString {
                    append("Status: ")
                    append(
                        when (status.backend) {
                            PrivilegeShell.Backend.ADB -> "Embedded ADB shell ready"
                            PrivilegeShell.Backend.SHIZUKU -> "External Shizuku ready"
                            PrivilegeShell.Backend.NONE -> "Not activated"
                        },
                    )
                    if (status.paired) append(" · paired before")
                    if (status.authRejected) append(" · key rejected (re-pair)")
                },
                style = MaterialTheme.typography.labelSmall,
            )

            FilledTonalButton(
                onClick = { AdbActivator.openWirelessDebuggingSettings(context) },
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("1. Open developer / wireless debugging")
            }

            Text(
                "2. Tap “Pair device with pairing code”, note the port + 6-digit code, then enter them below.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = pairPort,
                    onValueChange = { pairPort = it.filter { ch -> ch.isDigit() }.take(5) },
                    label = { Text("Pair port") },
                    modifier = Modifier.weight(1f),
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                    enabled = !busy,
                )
                OutlinedTextField(
                    value = pairCode,
                    onValueChange = { pairCode = it.filter { ch -> ch.isDigit() }.take(6) },
                    label = { Text("6-digit code") },
                    modifier = Modifier.weight(1f),
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword),
                    enabled = !busy,
                )
            }

            Button(
                onClick = {
                    val port = pairPort.toIntOrNull() ?: 0
                    busy = true
                    message = "Pairing…"
                    scope.launch {
                        val result = AdbActivator.pair(context, port, pairCode)
                        busy = false
                        status = AdbActivator.status(context)
                        result.onSuccess {
                            message = it
                            onActivated()
                        }.onFailure {
                            message = it.message ?: "Pair failed"
                        }
                    }
                },
                enabled = !busy && pairPort.isNotBlank() && pairCode.length == 6,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(if (busy) "Working…" else "3. Pair & connect shell")
            }

            FilledTonalButton(
                onClick = {
                    busy = true
                    message = "Reconnecting…"
                    scope.launch {
                        val ok = AdbActivator.reconnect(context)
                        busy = false
                        status = AdbActivator.status(context)
                        message = if (ok) "Shell reconnected" else "Reconnect failed — keep Wireless debugging ON"
                        if (ok) onActivated()
                    }
                },
                enabled = !busy,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("Reconnect (already paired this boot)")
            }

            if (message.isNotBlank()) {
                Text(message, style = MaterialTheme.typography.bodyMedium)
            }

            Spacer(Modifier.height(20.dp))
        }
    }
}

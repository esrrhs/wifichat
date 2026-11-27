package com.esrrhs.wifichat

import android.Manifest
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.OpenableColumns
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.core.content.ContextCompat
import androidx.lifecycle.lifecycleScope
import com.esrrhs.wifichat.ui.ChatScreen
import com.esrrhs.wifichat.ui.ChatViewModel
import com.esrrhs.wifichat.ui.theme.WifiChatTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.io.FileOutputStream

class MainActivity : ComponentActivity() {
    private val vm: ChatViewModel by viewModels()

    private val permissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) {
        vm.refreshPrivilege()
    }

    private val fileLauncher = registerForActivityResult(
        ActivityResultContracts.OpenDocument(),
    ) { uri: Uri? ->
        if (uri == null) return@registerForActivityResult
        lifecycleScope.launch {
            val path = withContext(Dispatchers.IO) { copyUriToCache(uri) } ?: return@launch
            vm.sendFile(path)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            WifiChatTheme {
                ChatScreen(
                    vm = vm,
                    onPickFile = {
                        fileLauncher.launch(arrayOf("*/*"))
                    },
                    onRequestPermissions = { ensurePermissions() },
                )
            }
        }
    }

    override fun onResume() {
        super.onResume()
        vm.refreshPrivilege()
    }

    private fun ensurePermissions() {
        val needed = buildList {
            add(Manifest.permission.ACCESS_FINE_LOCATION)
            add(Manifest.permission.ACCESS_COARSE_LOCATION)
            if (Build.VERSION.SDK_INT >= 33) {
                add(Manifest.permission.NEARBY_WIFI_DEVICES)
                add(Manifest.permission.POST_NOTIFICATIONS)
            }
        }.filter {
            ContextCompat.checkSelfPermission(this, it) != PackageManager.PERMISSION_GRANTED
        }
        if (needed.isNotEmpty()) {
            permissionLauncher.launch(needed.toTypedArray())
        }
    }

    private fun copyUriToCache(uri: Uri): String? {
        val name = contentResolver.query(uri, null, null, null, null)?.use { c ->
            val idx = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
            if (c.moveToFirst() && idx >= 0) c.getString(idx) else null
        } ?: "upload.bin"
        val safe = name.replace(Regex("[^A-Za-z0-9._-]"), "_")
        val outFile = File(cacheDir, "upload-$safe")
        contentResolver.openInputStream(uri)?.use { input ->
            FileOutputStream(outFile).use { output -> input.copyTo(output) }
        } ?: return null
        return outFile.absolutePath
    }
}

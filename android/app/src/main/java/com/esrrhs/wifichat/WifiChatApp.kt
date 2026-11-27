package com.esrrhs.wifichat

import android.app.Application
import com.wirelessdebug.WdbContext
import com.wirelessdebug.service.AdbKeyStore

class WifiChatApp : Application() {
    override fun onCreate() {
        super.onCreate()
        WdbContext.init(this)
        AdbKeyStore.ensureAssets(this)
    }
}

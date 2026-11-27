package com.esrrhs.wifichat

import com.esrrhs.wifichat.wifi.AndroidWifiHost
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class WifiScanParseTest {
    @Test
    fun parseCmdScan_extractsBeacons() {
        val text = """
            BSSID Frequency RSSI Age SSID Flags
            02:11:22:33:44:55 2462 -51 0.120 ANDROID_SSID_0123456789 [WPA2]
            aa:bb:cc:dd:ee:ff 2412 -60 0.010 HelloWorld [ESS]
        """.trimIndent()
        val arr = AndroidWifiHost.parseCmdScan(text)
        assertEquals(2, arr.size)
        assertEquals("ANDROID_SSID_0123456789", arr[0].ssid)
        assertEquals(11, arr[0].channel)
        assertEquals(-51, arr[0].signal)
        assertTrue(arr[1].ssid == "HelloWorld")
        val json = AndroidWifiHost.beaconsToJson(arr)
        assertTrue(json.contains("\"ssid\":\"HelloWorld\""))
    }

    @Test
    fun freqToChannel_maps24g() {
        assertEquals(6, AndroidWifiHost.freqToChannel(2437))
        assertEquals(1, AndroidWifiHost.freqToChannel(2412))
    }
}

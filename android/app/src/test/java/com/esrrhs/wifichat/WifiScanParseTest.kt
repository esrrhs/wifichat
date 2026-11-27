package com.esrrhs.wifichat

import com.esrrhs.wifichat.wifi.AndroidWifiHost
import com.esrrhs.wifichat.wifi.ScanBeacon
import com.esrrhs.wifichat.wifi.WifiShellUserService
import org.junit.Assert.assertArrayEquals
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
    fun beaconsToJson_escapesSpecialChars() {
        val json = AndroidWifiHost.beaconsToJson(
            listOf(ScanBeacon("a\"b\\c", "00:11:22:33:44:55", -42, 6)),
        )
        assertEquals(
            "[{\"ssid\":\"a\\\"b\\\\c\",\"bssid\":\"00:11:22:33:44:55\",\"signal\":-42,\"channel\":6}]",
            json,
        )
        assertEquals("[]", AndroidWifiHost.beaconsToJson(emptyList()))
    }

    @Test
    fun freqToChannel_maps24gAnd5g() {
        assertEquals(6, AndroidWifiHost.freqToChannel(2437))
        assertEquals(1, AndroidWifiHost.freqToChannel(2412))
        assertEquals(14, AndroidWifiHost.freqToChannel(2484))
        assertEquals(36, AndroidWifiHost.freqToChannel(5180))
        assertEquals(149, AndroidWifiHost.freqToChannel(5745))
        assertEquals(0, AndroidWifiHost.freqToChannel(900))
    }

    @Test
    fun wifiShellUserService_buildsExpectedCmdArgs() {
        assertArrayEquals(
            arrayOf("cmd", "wifi", "start-softap", "WC_TEST", "wpa2", "pass1234", "-b", "2"),
            WifiShellUserService.argsForSoftAp("WC_TEST", "pass1234"),
        )
        assertArrayEquals(arrayOf("cmd", "wifi", "stop-softap"), WifiShellUserService.argsStopSoftAp())
        assertArrayEquals(arrayOf("cmd", "wifi", "start-scan"), WifiShellUserService.argsStartScan())
        assertArrayEquals(arrayOf("cmd", "wifi", "list-scan-results"), WifiShellUserService.argsListScan())
    }
}

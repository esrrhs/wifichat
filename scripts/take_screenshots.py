#!/usr/bin/env python3
import re
import subprocess
import sys
import time
import xml.etree.ElementTree as ET

ADB = "adb"
PKG = "com.esrrhs.wifichat"

def adb(*args, check=True):
    res = subprocess.run([ADB, *args], capture_output=True, text=True, errors="ignore", timeout=30)
    if check and res.returncode != 0:
        raise RuntimeError(f"adb {' '.join(args)} failed: {res.stderr or res.stdout}")
    return res.stdout

def sh(cmd):
    return adb("shell", cmd, check=False)

def screencap(path):
    res = subprocess.run([ADB, "exec-out", "screencap", "-p"], capture_output=True, timeout=30)
    if res.returncode != 0 or not res.stdout:
        raise RuntimeError(f"screencap failed for {path}")
    with open(path, "wb") as f:
        f.write(res.stdout)
    print(f"Saved {path} ({len(res.stdout)} bytes)")

def dump_ui():
    sh("uiautomator dump /sdcard/ui.xml")
    xml_text = adb("shell", "cat /sdcard/ui.xml")
    return ET.fromstring(xml_text)

def find_and_tap(match_fn, desc):
    for _ in range(5):
        root = dump_ui()
        for node in root.iter("node"):
            if match_fn(node):
                bounds = node.attrib.get("bounds", "")
                m = re.match(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", bounds)
                if m:
                    x1, y1, x2, y2 = map(int, m.groups())
                    cx, cy = (x1 + x2) // 2, (y1 + y2) // 2
                    print(f"Tapping {desc} at ({cx}, {cy})")
                    sh(f"input tap {cx} {cy}")
                    time.sleep(1.5)
                    return True
        time.sleep(1.0)
    print(f"Warning: could not find {desc}", file=sys.stderr)
    return False

def main():
    # Wait for boot loadavg to settle
    for _ in range(25):
        raw = sh("cat /proc/loadavg").strip()
        load = raw.split()[0] if raw else "99"
        print(f"loadavg={load}")
        try:
            if float(load) < 6.0:
                break
        except ValueError:
            pass
        time.sleep(3)

    # Set exact 9:16 resolution for Google Play phone screenshots
    sh("wm size 1080x1920")
    sh("wm density 420")
    sh("settings put global window_animation_scale 0")
    sh("settings put global transition_animation_scale 0")
    sh("settings put global animator_duration_scale 0")

    # Unlock screen and grant runtime permissions so main UI is clean
    sh("input keyevent 82")
    for perm in [
        "android.permission.ACCESS_FINE_LOCATION",
        "android.permission.ACCESS_COARSE_LOCATION",
        "android.permission.NEARBY_WIFI_DEVICES",
        "android.permission.POST_NOTIFICATIONS",
    ]:
        sh(f"pm grant {PKG} {perm}")

    sh(f"am start -n {PKG}/.MainActivity")
    time.sleep(4)

    # 1. Home screen
    screencap("phone-1-home.png")

    # 2. Peers dialog
    if find_and_tap(lambda n: n.attrib.get("content-desc") == "Peers", "Peers"):
        screencap("phone-2-peers.png")
        sh("input keyevent 4")  # Back
        time.sleep(1.2)

    # 3. Settings sheet
    if find_and_tap(lambda n: n.attrib.get("content-desc") == "Settings", "Settings"):
        screencap("phone-3-settings.png")

        # 4. Activate SoftAP TX sheet
        if find_and_tap(lambda n: "Activate SoftAP TX" in n.attrib.get("text", ""), "Activate SoftAP TX"):
            screencap("phone-4-activate.png")

if __name__ == "__main__":
    main()

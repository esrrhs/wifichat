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
    sh("rm -f /sdcard/ui.xml")
    sh("uiautomator dump /sdcard/ui.xml")
    xml_text = adb("shell", "cat /sdcard/ui.xml", check=False)
    if not xml_text.strip().startswith("<"):
        return None
    return ET.fromstring(xml_text)

def has_text(needle):
    root = dump_ui()
    if root is None:
        return False
    for node in root.iter("node"):
        txt = (node.attrib.get("text", "") + " " + node.attrib.get("content-desc", ""))
        if needle in txt:
            return True
    return False

def wait_for_text(needle, timeout_s=25):
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        try:
            if has_text(needle):
                print(f"Verified UI text: {needle!r}")
                return True
        except Exception as e:
            print(f"wait_for_text({needle!r}) err: {e}")
        time.sleep(1.5)
    print(f"Warning: timed out waiting for {needle!r}", file=sys.stderr)
    return False

def tap_and_wait(match_fn, desc, expect_text, retries=4):
    for attempt in range(retries):
        try:
            root = dump_ui()
            if root is not None:
                for node in root.iter("node"):
                    if match_fn(node):
                        bounds = node.attrib.get("bounds", "")
                        m = re.match(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", bounds)
                        if m:
                            x1, y1, x2, y2 = map(int, m.groups())
                            cx, cy = (x1 + x2) // 2, (y1 + y2) // 2
                            print(f"Attempt {attempt+1}: tapping {desc} at ({cx}, {cy})")
                            sh(f"input tap {cx} {cy}")
                            if wait_for_text(expect_text, timeout_s=10):
                                time.sleep(1.0)
                                return True
        except Exception as e:
            print(f"tap_and_wait({desc}) err: {e}")
        time.sleep(1.0)
    raise RuntimeError(f"Failed to open {desc} (expected {expect_text!r})")

def main():
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

    sh("wm size 1080x1920")
    sh("wm density 420")
    sh("settings put global window_animation_scale 0")
    sh("settings put global transition_animation_scale 0")
    sh("settings put global animator_duration_scale 0")

    sh("input keyevent 82")
    for perm in [
        "android.permission.ACCESS_FINE_LOCATION",
        "android.permission.ACCESS_COARSE_LOCATION",
        "android.permission.NEARBY_WIFI_DEVICES",
        "android.permission.POST_NOTIFICATIONS",
    ]:
        sh(f"pm grant {PKG} {perm}")

    sh(f"am start -n {PKG}/.MainActivity --ez demo true")
    wait_for_text("Anyone nearby?", timeout_s=30)
    time.sleep(2.0)

    # 1. Home Public chatroom screen
    screencap("phone-1-home.png")

    # 2. Room tab
    tap_and_wait(lambda n: n.attrib.get("text") == "Room", "Room tab", "Enter room passphrase")
    screencap("phone-2-room.png")

    # 3. Help sheet
    tap_and_wait(lambda n: n.attrib.get("content-desc") == "Help", "Help", "How to use WiFiChat")
    screencap("phone-3-help.png")
    sh("input keyevent 4")
    time.sleep(1.5)

    # 4. Settings sheet
    tap_and_wait(lambda n: n.attrib.get("content-desc") == "Settings", "Settings", "Air settings")
    screencap("phone-4-settings.png")

if __name__ == "__main__":
    main()

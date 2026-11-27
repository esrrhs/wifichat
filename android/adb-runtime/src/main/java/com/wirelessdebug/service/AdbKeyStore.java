package com.wirelessdebug.service;

import android.content.Context;
import android.util.Log;

import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;

/**
 * Releases the app-private ADB client identity (RSA key + cert) used for
 * wireless-debugging pairing and STLS shell sessions.
 */
public final class AdbKeyStore {
    private static final String TAG = "AdbKeyStore";

    /** Private files subdirectory. Kept as "shizuku" name only for source compatibility
     *  with the upstream wireless-debug controllers; no Shizuku APK is shipped. */
    public static final String ASSET_DIR = "shizuku";
    public static final String ADB_KEY = "adbkey";
    public static final String ADB_CERT = "adbcert.pem";

    private static final String ASSET_PREFIX = "adb/";

    private AdbKeyStore() {}

    /** Idempotently copy key/cert from APK assets into filesDir. */
    public static void ensureAssets(Context ctx) {
        try {
            File dir = new File(ctx.getFilesDir(), ASSET_DIR);
            if (!dir.exists() && !dir.mkdirs()) return;
            copyIfNeeded(ctx, ADB_KEY, new File(dir, ADB_KEY));
            copyIfNeeded(ctx, ADB_CERT, new File(dir, ADB_CERT));
        } catch (Throwable t) {
            Log.e(TAG, "ensureAssets failed", t);
        }
    }

    public static File keyFile(Context ctx) {
        return new File(new File(ctx.getFilesDir(), ASSET_DIR), ADB_KEY);
    }

    public static File certFile(Context ctx) {
        return new File(new File(ctx.getFilesDir(), ASSET_DIR), ADB_CERT);
    }

    private static void copyIfNeeded(Context ctx, String name, File dst) throws Exception {
        if (dst.exists() && dst.length() > 0) {
            try (InputStream cur = ctx.getAssets().open(ASSET_PREFIX + name)) {
                int curLen = cur.available();
                if (dst.length() == curLen) return;
            }
        }
        try (InputStream in = ctx.getAssets().open(ASSET_PREFIX + name);
             FileOutputStream out = new FileOutputStream(dst)) {
            byte[] buf = new byte[8192];
            int n;
            while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
            out.flush();
        }
        Log.d(TAG, "released " + name);
    }
}

package com.wirelessdebug.service;

import org.junit.Test;

import java.lang.reflect.Field;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public class AdbShellControllerTest {

    @Test
    public void isConnected_doesNotBlockWhenLockHeld() throws Exception {
        Field lockField = AdbShellController.class.getDeclaredField("LOCK");
        lockField.setAccessible(true);
        Object lock = lockField.get(null);

        CountDownLatch lockAcquired = new CountDownLatch(1);
        CountDownLatch releaseLock = new CountDownLatch(1);
        Thread holder = new Thread(() -> {
            synchronized (lock) {
                lockAcquired.countDown();
                try {
                    releaseLock.await(5, TimeUnit.SECONDS);
                } catch (InterruptedException ignored) {
                }
            }
        });
        holder.start();
        assertTrue(lockAcquired.await(2, TimeUnit.SECONDS));

        AtomicBoolean completed = new AtomicBoolean(false);
        CountDownLatch probeDone = new CountDownLatch(1);
        Thread uiProbe = new Thread(() -> {
            AdbShellController.isConnected();
            completed.set(true);
            probeDone.countDown();
        });
        uiProbe.start();

        boolean finishedQuickly = probeDone.await(500, TimeUnit.MILLISECONDS);
        releaseLock.countDown();
        holder.join(2000);
        uiProbe.join(2000);

        assertTrue("isConnected() must not block on AdbShellController.LOCK", finishedQuickly && completed.get());
    }

    @Test
    public void looksLikeError_detectsCommonShellErrors() {
        assertFalse(AdbShellController.looksLikeError(null));
        assertFalse(AdbShellController.looksLikeError(""));
        assertFalse(AdbShellController.looksLikeError("SoftAP started successfully"));
        assertTrue(AdbShellController.looksLikeError("java.lang.SecurityException: Permission denial"));
        assertTrue(AdbShellController.looksLikeError("Error: unknown command"));
        assertTrue(AdbShellController.looksLikeError("cmd: not found"));
    }
}

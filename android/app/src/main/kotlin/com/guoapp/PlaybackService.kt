package com.guoapp

import android.app.Notification
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder

class PlaybackService : Service() {
    companion object {
        const val ACTION_DETACH = "com.guoapp.PLAYBACK_DETACH"
    }

    private var foreground = false

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_DETACH) {
            if (foreground) stopForeground(STOP_FOREGROUND_DETACH)
            foreground = false
            stopSelf()
            return START_NOT_STICKY
        }
        val notification = MediaControls.notification ?: placeholder()
        foreground = runCatching {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                startForeground(
                    MediaControls.NOTIFICATION_ID,
                    notification,
                    ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK
                )
            } else {
                startForeground(MediaControls.NOTIFICATION_ID, notification)
            }
        }.isSuccess
        if (!foreground || MediaControls.notification == null) stopSelf()
        return START_NOT_STICKY
    }

    override fun onTaskRemoved(rootIntent: Intent?) {
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun placeholder(): Notification {
        val builder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Notification.Builder(this, MediaControls.CHANNEL_ID)
        } else {
            @Suppress("DEPRECATION")
            Notification.Builder(this)
        }
        return builder.setSmallIcon(R.drawable.ic_pip_play).setContentTitle(applicationInfo.loadLabel(packageManager)).build()
    }
}

package com.guoapp

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.drawable.Icon
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.media.MediaMetadata
import android.media.session.MediaSession
import android.media.session.PlaybackState
import android.net.wifi.WifiManager
import android.os.Build
import android.os.PowerManager
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodChannel

private const val MEDIA_CONTROL_ACTION = "com.guoapp.MEDIA_CONTROL"
private const val ARTWORK_EXTENT = 512

class MediaControls(context: Context, messenger: BinaryMessenger) {
    companion object {
        const val NOTIFICATION_ID = 2407
        const val CHANNEL_ID = "zhenguojian_playback"

        @Volatile
        var notification: Notification? = null
            private set
    }

    private val context = context.applicationContext
    private val channel = MethodChannel(messenger, "duanju/media")
    private val notifications = this.context.getSystemService(NotificationManager::class.java)
    private val audio = this.context.getSystemService(AudioManager::class.java)
    private val session = MediaSession(this.context, "guoapp")
    private val wakeLock = this.context.getSystemService(PowerManager::class.java)
        .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "guoapp:playback")
        .apply { setReferenceCounted(false) }
    @Suppress("DEPRECATION")
    private val wifiLock = this.context.getSystemService(WifiManager::class.java)
        .createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "guoapp:playback")
        .apply { setReferenceCounted(false) }

    private var title = ""
    private var subtitle = ""
    private var artworkPath = ""
    private var artwork: Bitmap? = null
    private var active = false
    private var hasPrevious = false
    private var hasNext = false
    private var remote = false
    private var shown = false
    private var dismissed = false
    private var serviceStarted = false
    private var focused = false
    private var resumeOnFocus = false
    private var noisyRegistered = false

    private val commandReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            when (val command = intent.getStringExtra("command")) {
                "dismiss" -> dismiss()
                null -> Unit
                else -> send(command)
            }
        }
    }

    private val noisyReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            if (intent.action == AudioManager.ACTION_AUDIO_BECOMING_NOISY) send("pause")
        }
    }

    private val focusListener = AudioManager.OnAudioFocusChangeListener { change ->
        when (change) {
            AudioManager.AUDIOFOCUS_LOSS -> {
                focused = false
                resumeOnFocus = false
                send("pause")
                if (!active && serviceStarted) detachService()
            }
            AudioManager.AUDIOFOCUS_LOSS_TRANSIENT -> {
                resumeOnFocus = resumeOnFocus || active
                send("pause")
            }
            AudioManager.AUDIOFOCUS_GAIN -> {
                if (resumeOnFocus) send("play")
                resumeOnFocus = false
            }
        }
    }

    private val focusRequest: AudioFocusRequest? = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
        AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN)
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_MEDIA)
                    .setContentType(AudioAttributes.CONTENT_TYPE_MOVIE)
                    .build()
            )
            .setOnAudioFocusChangeListener(focusListener)
            .build()
    } else null

    init {
        @Suppress("DEPRECATION")
        session.setFlags(MediaSession.FLAG_HANDLES_MEDIA_BUTTONS or MediaSession.FLAG_HANDLES_TRANSPORT_CONTROLS)
        session.setCallback(object : MediaSession.Callback() {
            override fun onPlay() = send("play")
            override fun onPause() = send("pause")
            override fun onStop() = send("pause")
            override fun onSkipToNext() = send("next")
            override fun onSkipToPrevious() = send("previous")
            override fun onSeekTo(pos: Long) = send("seek", pos)
        })
        session.setSessionActivity(launchIntent())
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            notifications.createNotificationChannel(
                NotificationChannel(CHANNEL_ID, "后台播放", NotificationManager.IMPORTANCE_LOW).apply {
                    description = "后台播放时的播放控制"
                    setShowBadge(false)
                }
            )
        }
        register(commandReceiver, IntentFilter(MEDIA_CONTROL_ACTION))
        channel.setMethodCallHandler { call, result ->
            when (call.method) {
                "update" -> {
                    update(call.arguments as? Map<*, *> ?: emptyMap<String, Any>())
                    result.success(null)
                }
                "clear" -> {
                    clear()
                    result.success(null)
                }
                else -> result.notImplemented()
            }
        }
    }

    fun dispose() {
        clear()
        channel.setMethodCallHandler(null)
        runCatching { context.unregisterReceiver(commandReceiver) }
        session.release()
    }

    private fun send(command: String, positionMs: Long? = null) {
        channel.invokeMethod(
            "command",
            if (positionMs == null) mapOf("command" to command)
            else mapOf("command" to command, "positionMs" to positionMs)
        )
    }

    private fun update(args: Map<*, *>) {
        val nextActive = args["active"] == true
        val nextPlaying = args["playing"] == true
        if (dismissed && !nextActive) return
        dismissed = false
        title = args["title"] as? String ?: ""
        subtitle = args["subtitle"] as? String ?: ""
        hasPrevious = args["hasPrevious"] == true
        hasNext = args["hasNext"] == true
        remote = args["remote"] == true
        val path = args["artwork"] as? String ?: ""
        if (path != artworkPath) {
            artworkPath = path
            artwork = decodeArtwork(path)
        }
        val position = (args["positionMs"] as? Number)?.toLong() ?: 0L
        val duration = (args["durationMs"] as? Number)?.toLong() ?: 0L
        val rate = (args["rate"] as? Number)?.toFloat()?.takeIf { it > 0f } ?: 1f
        session.setMetadata(
            MediaMetadata.Builder()
                .putString(MediaMetadata.METADATA_KEY_TITLE, title)
                .putString(MediaMetadata.METADATA_KEY_DISPLAY_TITLE, title)
                .putString(MediaMetadata.METADATA_KEY_ARTIST, subtitle)
                .putString(MediaMetadata.METADATA_KEY_DISPLAY_SUBTITLE, subtitle)
                .putLong(MediaMetadata.METADATA_KEY_DURATION, if (duration > 0) duration else -1L)
                .apply { artwork?.let { putBitmap(MediaMetadata.METADATA_KEY_ART, it) } }
                .build()
        )
        var actions = PlaybackState.ACTION_PLAY or PlaybackState.ACTION_PAUSE or
            PlaybackState.ACTION_PLAY_PAUSE or PlaybackState.ACTION_STOP
        if (duration > 0) actions = actions or PlaybackState.ACTION_SEEK_TO
        if (hasPrevious) actions = actions or PlaybackState.ACTION_SKIP_TO_PREVIOUS
        if (hasNext) actions = actions or PlaybackState.ACTION_SKIP_TO_NEXT
        session.setPlaybackState(
            PlaybackState.Builder()
                .setActions(actions)
                .setState(
                    if (nextPlaying) PlaybackState.STATE_PLAYING else PlaybackState.STATE_PAUSED,
                    position,
                    rate
                )
                .build()
        )
        session.isActive = true
        setActive(nextActive)
        publish()
    }

    private fun setActive(value: Boolean) {
        active = value
        if (value && !remote) {
            requestFocus()
            if (!noisyRegistered) {
                register(noisyReceiver, IntentFilter(AudioManager.ACTION_AUDIO_BECOMING_NOISY))
                noisyRegistered = true
            }
        } else {
            unregisterNoisy()
        }
        if (remote) abandonFocus()
        if (value) {
            if (!wakeLock.isHeld) wakeLock.acquire()
            if (!wifiLock.isHeld) wifiLock.acquire()
        } else {
            releaseLocks()
        }
    }

    private fun publish() {
        val built = buildNotification()
        notification = built
        if (active || resumeOnFocus) {
            if (!serviceStarted) {
                val intent = Intent(context, PlaybackService::class.java)
                serviceStarted = runCatching {
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                        context.startForegroundService(intent)
                    } else {
                        context.startService(intent)
                    }
                }.isSuccess
            }
        } else if (serviceStarted) {
            detachService()
        }
        notifications.notify(NOTIFICATION_ID, built)
        shown = true
    }

    private fun detachService() {
        serviceStarted = false
        runCatching {
            context.startService(
                Intent(context, PlaybackService::class.java).setAction(PlaybackService.ACTION_DETACH)
            )
        }
    }

    private fun stopService() {
        serviceStarted = false
        runCatching { context.stopService(Intent(context, PlaybackService::class.java)) }
    }

    private fun dismiss() {
        dismissed = true
        if (active) send("pause")
        hide()
    }

    private fun clear() {
        dismissed = false
        hide()
    }

    private fun hide() {
        session.isActive = false
        setActive(false)
        abandonFocus()
        resumeOnFocus = false
        if (serviceStarted) stopService()
        if (shown) notifications.cancel(NOTIFICATION_ID)
        shown = false
        notification = null
    }

    private fun buildNotification(): Notification {
        val builder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Notification.Builder(context, CHANNEL_ID)
        } else {
            @Suppress("DEPRECATION")
            Notification.Builder(context)
        }
        val actions = buildList {
            if (hasPrevious) add(action(1, "previous", R.drawable.ic_pip_previous, "上一集"))
            add(
                if (active) action(2, "pause", R.drawable.ic_pip_pause, "暂停")
                else action(2, "play", R.drawable.ic_pip_play, "播放")
            )
            if (hasNext) add(action(3, "next", R.drawable.ic_pip_next, "下一集"))
        }
        actions.forEach(builder::addAction)
        return builder
            .setSmallIcon(R.drawable.ic_pip_play)
            .setContentTitle(title)
            .setContentText(subtitle)
            .setLargeIcon(artwork)
            .setContentIntent(launchIntent())
            .setDeleteIntent(commandIntent(4, "dismiss"))
            .setVisibility(Notification.VISIBILITY_PUBLIC)
            .setCategory(Notification.CATEGORY_TRANSPORT)
            .setOngoing(active)
            .setShowWhen(false)
            .setOnlyAlertOnce(true)
            .setStyle(
                Notification.MediaStyle()
                    .setMediaSession(session.sessionToken)
                    .setShowActionsInCompactView(*actions.indices.toList().toIntArray())
            )
            .build()
    }

    private fun action(request: Int, command: String, icon: Int, label: String): Notification.Action =
        Notification.Action.Builder(Icon.createWithResource(context, icon), label, commandIntent(request, command))
            .build()

    private fun commandIntent(request: Int, command: String): PendingIntent = PendingIntent.getBroadcast(
        context,
        request,
        Intent(MEDIA_CONTROL_ACTION).setPackage(context.packageName).putExtra("command", command),
        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
    )

    private fun launchIntent(): PendingIntent = PendingIntent.getActivity(
        context,
        0,
        Intent(context, MainActivity::class.java)
            .setAction(Intent.ACTION_MAIN)
            .addCategory(Intent.CATEGORY_LAUNCHER)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
    )

    private fun requestFocus() {
        if (focused) return
        val result = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            audio.requestAudioFocus(focusRequest!!)
        } else {
            @Suppress("DEPRECATION")
            audio.requestAudioFocus(focusListener, AudioManager.STREAM_MUSIC, AudioManager.AUDIOFOCUS_GAIN)
        }
        focused = result == AudioManager.AUDIOFOCUS_REQUEST_GRANTED
    }

    private fun abandonFocus() {
        if (!focused) return
        focused = false
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            audio.abandonAudioFocusRequest(focusRequest!!)
        } else {
            @Suppress("DEPRECATION")
            audio.abandonAudioFocus(focusListener)
        }
    }

    private fun unregisterNoisy() {
        if (!noisyRegistered) return
        noisyRegistered = false
        runCatching { context.unregisterReceiver(noisyReceiver) }
    }

    private fun releaseLocks() {
        if (wakeLock.isHeld) wakeLock.release()
        if (wifiLock.isHeld) wifiLock.release()
    }

    private fun register(receiver: BroadcastReceiver, filter: IntentFilter) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            context.registerReceiver(receiver, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            @Suppress("UnspecifiedRegisterReceiverFlag")
            context.registerReceiver(receiver, filter)
        }
    }

    private fun decodeArtwork(path: String): Bitmap? {
        if (path.isEmpty()) return null
        return runCatching {
            val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeFile(path, bounds)
            var sample = 1
            while (maxOf(bounds.outWidth, bounds.outHeight) / (sample * 2) >= ARTWORK_EXTENT) sample *= 2
            BitmapFactory.decodeFile(path, BitmapFactory.Options().apply { inSampleSize = sample })
        }.getOrNull()
    }
}

package org.netgo.client

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.VpnService
import android.os.Build
import androidx.core.app.NotificationCompat
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.collect
import org.netgo.runtime.mobile.Client
import org.netgo.runtime.mobile.Mobile

class NetGoVpnService : VpnService() {
    @Volatile private var engine: Client? = null
    private val lifecycle = Any()
    private var destroyed = false
    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private lateinit var platform: VpnPlatform
    private lateinit var manager: ConnectivityManager
    private val changes = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) { engine?.networkChanged(true, platform.networkDNS()) }
        override fun onLinkPropertiesChanged(network: Network, properties: android.net.LinkProperties) { engine?.networkChanged(true, platform.networkDNS()) }
        override fun onLost(network: Network) { val available = manager.allNetworks.any { manager.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == false }; engine?.networkChanged(available, platform.networkDNS()) }
    }
    override fun onCreate() {
        super.onCreate(); platform = VpnPlatform(this); manager = getSystemService(ConnectivityManager::class.java)
        serviceScope.launch {
            NetGoRuntime.running.collect { active ->
                if (!active) withContext(Dispatchers.Main) { if (!NetGoRuntime.running.value) stopForeground(STOP_FOREGROUND_REMOVE) }
            }
        }
        manager.registerNetworkCallback(NetworkRequest.Builder().addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET).addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN).build(), changes)
        serviceScope.launch {
            try {
                val value = Mobile.newClient(filesDir.absolutePath, platform, Build.VERSION.SDK_INT.toLong(), Build.MODEL)
                val accepted = synchronized(lifecycle) { if (destroyed) false else { engine=value; NetGoRuntime.attach(value); true } }
                if (!accepted) runCatching { value.close() }
            } catch (error: Exception) { synchronized(lifecycle) { if (!destroyed) NetGoRuntime.message.value = error.message ?: "客户端启动失败" } }
        }
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == "connect") {
            if (prepare(this) != null) { NetGoRuntime.message.value = "请先允许 VPN 连接"; stopSelf(); return START_NOT_STICKY }
            val notifications = getSystemService(NotificationManager::class.java)
            notifications.createNotificationChannel(NotificationChannel("netgo-vpn", "NetGo 网络连接", NotificationManager.IMPORTANCE_LOW))
            val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
            val notification = NotificationCompat.Builder(this, "netgo-vpn").setSmallIcon(R.drawable.ic_launcher_foreground).setContentTitle("NetGo").setContentText("网络连接运行中").setContentIntent(open).setOngoing(true).build()
            if (Build.VERSION.SDK_INT >= 34) startForeground(1, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED) else startForeground(1, notification)
            NetGoRuntime.connect(intent.getStringExtra("site") ?: "", intent.getStringExtra("alias") ?: "", intent.getStringExtra("password") ?: "", intent.getBooleanExtra("internet", false), intent.getBooleanExtra("replace", false)); intent.removeExtra("password")
        }
        return START_NOT_STICKY
    }
    override fun onRevoke() { NetGoRuntime.disconnect(); stopSelf(); super.onRevoke() }
    override fun onDestroy() {
        synchronized(lifecycle) { destroyed=true; engine?.let { NetGoRuntime.detach(it) }; engine=null }
        serviceScope.cancel(); manager.unregisterNetworkCallback(changes); super.onDestroy()
    }
}

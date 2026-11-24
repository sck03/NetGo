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
import kotlinx.coroutines.launch
import org.netgo.runtime.mobile.Client
import org.netgo.runtime.mobile.Mobile

class NetGoVpnService : VpnService() {
    private var engine: Client? = null
    private lateinit var platform: VpnPlatform
    private lateinit var manager: ConnectivityManager
    private val changes = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) { engine?.networkChanged(true, platform.networkDNS()) }
        override fun onLinkPropertiesChanged(network: Network, properties: android.net.LinkProperties) { engine?.networkChanged(true, platform.networkDNS()) }
        override fun onLost(network: Network) { val available = manager.allNetworks.any { manager.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == false }; engine?.networkChanged(available, platform.networkDNS()) }
    }
    override fun onCreate() {
        super.onCreate(); platform = VpnPlatform(this); manager = getSystemService(ConnectivityManager::class.java)
        manager.registerNetworkCallback(NetworkRequest.Builder().addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET).addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN).build(), changes)
        NetGoRuntime.scope.launch {
            try { val value = Mobile.newClient(filesDir.absolutePath, platform, Build.VERSION.SDK_INT.toLong(), Build.MODEL); engine = value; NetGoRuntime.attach(value) }
            catch (error: Exception) { NetGoRuntime.message.value = error.message ?: "客户端启动失败" }
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
            NetGoRuntime.connect(intent.getStringExtra("site") ?: "", intent.getStringExtra("alias") ?: "", intent.getStringExtra("password") ?: "", intent.getBooleanExtra("internet", false)); intent.removeExtra("password")
        }
        return START_NOT_STICKY
    }
    override fun onRevoke() { NetGoRuntime.disconnect(); stopSelf(); super.onRevoke() }
    override fun onDestroy() { manager.unregisterNetworkCallback(changes); engine?.let { NetGoRuntime.detach(it) }; engine = null; super.onDestroy() }
}

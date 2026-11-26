package org.netgo.client

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import org.netgo.runtime.mobile.Platform
import java.io.File
import java.net.NetworkInterface
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class VpnPlatform(private val service: NetGoVpnService) : Platform {
    private val folder = File(service.filesDir, "device-keys").apply { mkdirs() }
    private val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
    private val alias = "NetGo.DeviceKeyEncryption"
	private var selectedDns = ""
    private var address = ""
    private fun key(): SecretKey = synchronized(store) {
        (store.getKey(alias, null) as? SecretKey) ?: KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build()); generateKey()
        }
    }
    private fun file(site: String): AtomicFile { require(site.matches(Regex("[A-Za-z0-9][A-Za-z0-9_.-]{0,62}"))); return AtomicFile(File(folder, "$site.gcm")) }
    override fun readKey(site: String): ByteArray {
        val target = file(site); if (!target.baseFile.exists()) return ByteArray(0)
        val bytes = target.readFully(); require(bytes.size in 28..2048)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, bytes.copyOfRange(0, 12)))
        return cipher.doFinal(bytes, 12, bytes.size - 12).also { require(it.size == 32) }
    }
    override fun writeKey(site: String, bytes: ByteArray) {
        require(bytes.size == 32)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.ENCRYPT_MODE, key())
        val target = file(site); val output = target.startWrite()
        try { output.write(cipher.iv); output.write(cipher.doFinal(bytes)); target.finishWrite(output) }
        catch (error: Exception) { target.failWrite(output); throw error }
    }
    @Synchronized override fun configureInterface(address: String, address6: String, mtu: Long, dns: String, search: String, routes: String): Long {
        this.address = address // configureDNS selects the site/system resolver explicitly.
        val builder = service.Builder().setSession("NetGo").setMtu(mtu.toInt().coerceIn(1280, 9000))
        fun add(value: String) { if (value.isNotEmpty()) { val fields = value.split('/'); require(fields.size == 2); builder.addAddress(fields[0], fields[1].toInt()) } }
        add(address); add(address6)
        routes.split(';').filter { it.isNotBlank() }.forEach { val fields = it.split('/'); require(fields.size == 2); builder.addRoute(fields[0], fields[1].toInt()) }
        selectedDns.split(';').filter { it.isNotBlank() }.forEach { builder.addDnsServer(it) }
        search.split(';').filter { it.isNotBlank() }.forEach { builder.addSearchDomain(it) }
        return builder.establish()?.use { it.detachFd().toLong() } ?: error("VPN 授权已失效")
    }
	@Synchronized override fun configureDNS(dns: String) { selectedDns = dns }
    @Synchronized override fun updateAddr(address: String) { if (this.address != address) { this.address = address; NetGoRuntime.client?.networkChanged(true, networkDNS()) } }
    override fun protectSocket(fd: Int): Boolean = service.protect(fd)
    override fun networkDNS(): String {
        val manager = service.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val network = manager.allNetworks.firstOrNull { manager.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == false } ?: return ""
        return manager.getLinkProperties(network)?.dnsServers?.mapNotNull { it.hostAddress }?.joinToString(";") ?: ""
    }
    override fun iFaces(): String = NetworkInterface.getNetworkInterfaces().toList().filter { !it.name.startsWith("tun") && it.isUp }.joinToString("\n") { iface ->
        val addresses = iface.interfaceAddresses.mapNotNull { a -> a.address.hostAddress?.takeUnless { '%' in it }?.let { "$it/${a.networkPrefixLength}" } }.joinToString(" ")
        "${iface.name} ${iface.index} ${iface.mtu} ${iface.isUp} true ${iface.isLoopback} ${iface.isPointToPoint} ${iface.supportsMulticast()}|$addresses"
    }
}

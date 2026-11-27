package com.esrrhs.wifichat.data

import android.content.Context
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

private val Context.dataStore by preferencesDataStore("wifichat_prefs")

data class ChatPrefs(
    val nick: String = "",
    val key: String = "",
    val stealth: Boolean = false,
    val rxOnly: Boolean = false,
    val prefix: String = "",
)

class UserPrefs(private val context: Context) {
    private val nickKey = stringPreferencesKey("nick")
    private val roomKey = stringPreferencesKey("room_key")
    private val stealthKey = booleanPreferencesKey("stealth")
    private val rxOnlyKey = booleanPreferencesKey("rx_only")
    private val prefixKey = stringPreferencesKey("prefix")

    val flow: Flow<ChatPrefs> = context.dataStore.data.map { p ->
        ChatPrefs(
            nick = p[nickKey].orEmpty(),
            key = p[roomKey].orEmpty(),
            stealth = p[stealthKey] ?: false,
            rxOnly = p[rxOnlyKey] ?: false,
            prefix = p[prefixKey].orEmpty(),
        )
    }

    suspend fun update(transform: (ChatPrefs) -> ChatPrefs) {
        context.dataStore.edit { prefs ->
            val cur = ChatPrefs(
                nick = prefs[nickKey].orEmpty(),
                key = prefs[roomKey].orEmpty(),
                stealth = prefs[stealthKey] ?: false,
                rxOnly = prefs[rxOnlyKey] ?: false,
                prefix = prefs[prefixKey].orEmpty(),
            )
            val next = transform(cur)
            prefs[nickKey] = next.nick
            prefs[roomKey] = next.key
            prefs[stealthKey] = next.stealth
            prefs[rxOnlyKey] = next.rxOnly
            prefs[prefixKey] = next.prefix
        }
    }
}

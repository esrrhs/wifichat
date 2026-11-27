package com.esrrhs.wifichat.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

private val SignalCyan = Color(0xFF2BB0ED)
private val SignalGreen = Color(0xFF3DDC97)
private val Ink = Color(0xFF0B1220)
private val Panel = Color(0xFF121A2B)
private val PanelHigh = Color(0xFF1A2438)
private val Mist = Color(0xFFB6C2D9)

private val Colors = darkColorScheme(
    primary = SignalCyan,
    onPrimary = Ink,
    secondary = SignalGreen,
    onSecondary = Ink,
    background = Ink,
    onBackground = Mist,
    surface = Panel,
    onSurface = Mist,
    surfaceVariant = PanelHigh,
    onSurfaceVariant = Color(0xFF93A4C3),
    outline = Color(0xFF2C3A55),
    error = Color(0xFFFF6B6B),
)

private val Type = Typography(
    displayLarge = TextStyle(
        fontFamily = FontFamily.Serif,
        fontWeight = FontWeight.Bold,
        fontSize = 40.sp,
        letterSpacing = (-0.5).sp,
    ),
    headlineMedium = TextStyle(
        fontFamily = FontFamily.Serif,
        fontWeight = FontWeight.SemiBold,
        fontSize = 22.sp,
    ),
    titleMedium = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Medium,
        fontSize = 16.sp,
    ),
    bodyLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Normal,
        fontSize = 16.sp,
        lineHeight = 22.sp,
    ),
    bodyMedium = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontSize = 14.sp,
        lineHeight = 20.sp,
    ),
    labelLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Medium,
        fontSize = 14.sp,
    ),
    labelSmall = TextStyle(
        fontFamily = FontFamily.Monospace,
        fontSize = 11.sp,
        letterSpacing = 0.4.sp,
    ),
)

@Composable
fun WifiChatTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = Colors,
        typography = Type,
        content = content,
    )
}

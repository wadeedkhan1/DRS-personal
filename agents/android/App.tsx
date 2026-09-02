import React, {useCallback, useEffect, useRef, useState} from 'react';
import {
  Alert,
  PermissionsAndroid,
  Platform,
  SafeAreaView,
  ScrollView,
  Share,
  StatusBar,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import {clearLogs, getLogText, subscribeLogs} from './src/log';

import {Connection, ConnStatus} from './src/net/connection';
import {defaultAcquireStream} from './src/webrtc/session';
import {enroll} from './src/net/enroll';
import {
  clearIdentity,
  Identity,
  loadIdentity,
  saveIdentity,
} from './src/config/storage';
import {
  getDeviceTelemetry,
  startConnectionService,
  stopConnectionService,
} from './src/telemetry';

const STATUS_LABEL: Record<ConnStatus, string> = {
  connecting: 'Connecting…',
  online: 'Online — available for monitoring',
  in_session: 'Live — sharing screen',
  reconnecting: 'Reconnecting…',
  fatal: 'Disconnected',
  stopped: 'Stopped',
};

async function ensureNotificationPermission(): Promise<void> {
  if (Platform.OS === 'android' && Platform.Version >= 33) {
    try {
      await PermissionsAndroid.request(
        PermissionsAndroid.PERMISSIONS.POST_NOTIFICATIONS,
      );
    } catch {
      // non-fatal; the foreground service still runs
    }
  }
}

export default function App() {
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [loading, setLoading] = useState(true);
  const [serverUrl, setServerUrl] = useState('http://192.168.1.100:8080');
  const [token, setToken] = useState('');
  const [enrolling, setEnrolling] = useState(false);
  const [status, setStatus] = useState<ConnStatus>('connecting');
  const [statusMessage, setStatusMessage] = useState<string | undefined>();
  const [battery, setBattery] = useState<number | null>(null);
  const [deviceModel, setDeviceModel] = useState<string>('');
  const [statLine, setStatLine] = useState<string | null>(null);
  const [logs, setLogs] = useState<string[]>([]);

  const connRef = useRef<Connection | null>(null);
  const logScrollRef = useRef<ScrollView | null>(null);

  // Mirror the shared logger into the UI.
  useEffect(() => subscribeLogs(setLogs), []);

  // Restore a saved identity on launch.
  useEffect(() => {
    (async () => {
      const id = await loadIdentity();
      setIdentity(id);
      setLoading(false);
    })();
    getDeviceTelemetry().then(t => {
      setBattery(t.batteryLevel);
      setDeviceModel(t.deviceModel);
    });
  }, []);

  const connect = useCallback(async (id: Identity) => {
    await ensureNotificationPermission();
    await startConnectionService();
    const conn = new Connection(id, {
      acquire: defaultAcquireStream,
      onStatus: (s, msg) => {
        setStatus(s);
        setStatusMessage(msg);
        if (s !== 'in_session') {
          setStatLine(null);
        }
      },
      onStat: setStatLine,
    });
    connRef.current = conn;
    conn.start();
  }, []);

  // Open the connection whenever we have an identity; tear it down on unmount / reset.
  useEffect(() => {
    if (!identity) {
      return;
    }
    connect(identity);
    return () => {
      connRef.current?.stop();
      connRef.current = null;
      stopConnectionService();
    };
  }, [identity, connect]);

  const handleEnroll = async () => {
    if (!serverUrl.trim()) {
      Alert.alert('Missing server', 'Enter the DRS server address.');
      return;
    }
    if (!token.trim()) {
      Alert.alert('Missing token', 'Enter the one-time enrollment token.');
      return;
    }
    setEnrolling(true);
    try {
      const t = await getDeviceTelemetry();
      const id = await enroll(serverUrl, token, t.deviceModel || 'Android Phone');
      await saveIdentity(id);
      setIdentity(id); // triggers connect
    } catch (e: any) {
      Alert.alert('Enrollment failed', e?.message ?? String(e));
    } finally {
      setEnrolling(false);
    }
  };

  const handleReset = () => {
    Alert.alert(
      'Reset agent',
      'This removes the enrolled identity from this device. You will need a new token to re-enroll.',
      [
        {text: 'Cancel', style: 'cancel'},
        {
          text: 'Reset',
          style: 'destructive',
          onPress: async () => {
            connRef.current?.stop();
            connRef.current = null;
            await stopConnectionService();
            await clearIdentity();
            setToken('');
            setIdentity(null);
          },
        },
      ],
    );
  };

  const shareLogs = async () => {
    try {
      await Share.share({message: getLogText() || '(no logs yet)'});
    } catch {
      // user cancelled
    }
  };

  const dotStyle =
    status === 'in_session'
      ? styles.dotGreen
      : status === 'online'
      ? styles.dotSky
      : status === 'fatal'
      ? styles.dotRed
      : styles.dotAmber;

  return (
    <SafeAreaView style={styles.container}>
      <StatusBar barStyle="light-content" backgroundColor="#020617" />
      <ScrollView contentContainerStyle={styles.content}>
        <View style={styles.header}>
          <Text style={styles.title}>DRS Endpoint Agent</Text>
          <Text style={styles.subtitle}>Android Screen Monitoring Service</Text>
        </View>

        {loading ? null : !identity ? (
          <View style={styles.card}>
            <Text style={styles.cardTitle}>Device Enrollment</Text>
            <Text style={styles.cardDesc}>
              Enter the DRS server address and the one-time enrollment token generated from
              the portal.
            </Text>

            <View style={styles.inputGroup}>
              <Text style={styles.label}>SERVER URL</Text>
              <TextInput
                style={styles.input}
                value={serverUrl}
                onChangeText={setServerUrl}
                placeholder="http://vps-ip:8080"
                placeholderTextColor="#64748b"
                autoCapitalize="none"
                autoCorrect={false}
                keyboardType="url"
              />
            </View>

            <View style={styles.inputGroup}>
              <Text style={styles.label}>ENROLLMENT TOKEN</Text>
              <TextInput
                style={styles.input}
                value={token}
                onChangeText={setToken}
                placeholder="DRS-XXXXXX"
                placeholderTextColor="#64748b"
                autoCapitalize="characters"
                autoCorrect={false}
              />
            </View>

            <TouchableOpacity
              style={[styles.primaryButton, enrolling && styles.buttonDisabled]}
              onPress={handleEnroll}
              disabled={enrolling}>
              <Text style={styles.buttonText}>
                {enrolling ? 'Enrolling…' : 'Enroll Device'}
              </Text>
            </TouchableOpacity>
          </View>
        ) : (
          <View style={styles.card}>
            <View style={styles.statusRow}>
              <View style={[styles.dot, dotStyle]} />
              <Text style={styles.statusText}>{STATUS_LABEL[status]}</Text>
            </View>

            {status === 'in_session' && (
              <Text style={styles.sessionHint}>
                {statusMessage ? `${statusMessage} is viewing. ` : ''}
                Approve the screen-capture prompt if it appears.
              </Text>
            )}
            {status === 'in_session' && statLine && (
              <Text style={styles.statLine}>{statLine}</Text>
            )}
            {status === 'fatal' && statusMessage ? (
              <Text style={styles.errorHint}>{statusMessage}</Text>
            ) : null}

            <View style={styles.infoBox}>
              <Text style={styles.infoLabel}>Device: {deviceModel || '—'}</Text>
              <Text style={styles.infoLabel}>
                Battery: {battery !== null ? `${battery}%` : '—'}
              </Text>
              <Text style={styles.infoLabel}>Capture: Android MediaProjection (VP8 / WebRTC)</Text>
              <Text style={styles.infoLabel}>Server: {identity.serverUrl}</Text>
            </View>

            <Text style={styles.note}>
              Capture is started remotely by an operator. Keep the app running; a
              notification keeps the connection alive in the background.
            </Text>

            <TouchableOpacity
              style={[styles.primaryButton, styles.resetButton]}
              onPress={handleReset}>
              <Text style={styles.buttonText}>Reset / Re-enroll</Text>
            </TouchableOpacity>
          </View>
        )}

        {/* Debug log panel */}
        {!loading && (
          <View style={styles.logCard}>
            <View style={styles.logHeader}>
              <Text style={styles.logTitle}>DEBUG LOG</Text>
              <View style={styles.logActions}>
                <TouchableOpacity onPress={shareLogs} style={styles.logBtn}>
                  <Text style={styles.logBtnText}>Share</Text>
                </TouchableOpacity>
                <TouchableOpacity onPress={() => clearLogs()} style={styles.logBtn}>
                  <Text style={styles.logBtnText}>Clear</Text>
                </TouchableOpacity>
              </View>
            </View>
            <ScrollView
              ref={logScrollRef}
              style={styles.logScroll}
              nestedScrollEnabled
              onContentSizeChange={() => logScrollRef.current?.scrollToEnd({animated: false})}>
              {logs.length === 0 ? (
                <Text style={styles.logEmpty}>No activity yet.</Text>
              ) : (
                logs.map((line, i) => (
                  <Text key={i} style={styles.logLine}>
                    {line}
                  </Text>
                ))
              )}
            </ScrollView>
          </View>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {flex: 1, backgroundColor: '#020617'},
  content: {flexGrow: 1, padding: 24, justifyContent: 'flex-start'},
  header: {alignItems: 'center', marginTop: 24, marginBottom: 24},
  title: {fontSize: 24, fontWeight: 'bold', color: '#f8fafc'},
  subtitle: {fontSize: 13, color: '#94a3b8', marginTop: 4},
  card: {
    backgroundColor: '#0f172a',
    borderRadius: 20,
    padding: 24,
    borderWidth: 1,
    borderColor: '#1e293b',
  },
  cardTitle: {fontSize: 18, fontWeight: 'bold', color: '#f8fafc', marginBottom: 6},
  cardDesc: {fontSize: 12, color: '#94a3b8', lineHeight: 18, marginBottom: 20},
  inputGroup: {marginBottom: 16},
  label: {
    fontSize: 10,
    fontWeight: '700',
    color: '#38bdf8',
    letterSpacing: 1,
    marginBottom: 6,
  },
  input: {
    backgroundColor: '#020617',
    borderWidth: 1,
    borderColor: '#334155',
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 12,
    color: '#f8fafc',
    fontSize: 14,
  },
  primaryButton: {
    backgroundColor: '#0284c7',
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: 'center',
    marginTop: 8,
  },
  resetButton: {backgroundColor: '#334155'},
  buttonDisabled: {opacity: 0.6},
  buttonText: {color: '#ffffff', fontWeight: '600', fontSize: 14},
  statusRow: {flexDirection: 'row', alignItems: 'center', marginBottom: 12},
  dot: {width: 10, height: 10, borderRadius: 5, marginRight: 10},
  dotGreen: {backgroundColor: '#22c55e'},
  dotSky: {backgroundColor: '#38bdf8'},
  dotAmber: {backgroundColor: '#f59e0b'},
  dotRed: {backgroundColor: '#ef4444'},
  statusText: {fontSize: 15, fontWeight: '600', color: '#f8fafc', flexShrink: 1},
  sessionHint: {fontSize: 12, color: '#22c55e', marginBottom: 12},
  statLine: {fontSize: 11, color: '#38bdf8', fontFamily: 'monospace', marginBottom: 12},
  errorHint: {fontSize: 12, color: '#f87171', marginBottom: 12},
  infoBox: {
    backgroundColor: '#020617',
    padding: 16,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: '#1e293b',
    marginBottom: 16,
  },
  infoLabel: {fontSize: 12, color: '#94a3b8', marginVertical: 2},
  note: {fontSize: 11, color: '#64748b', lineHeight: 16, marginBottom: 16},
  logCard: {
    marginTop: 20,
    backgroundColor: '#0b1220',
    borderRadius: 16,
    borderWidth: 1,
    borderColor: '#1e293b',
    overflow: 'hidden',
  },
  logHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderBottomWidth: 1,
    borderBottomColor: '#1e293b',
  },
  logTitle: {fontSize: 10, fontWeight: '700', color: '#38bdf8', letterSpacing: 1},
  logActions: {flexDirection: 'row', gap: 8},
  logBtn: {
    paddingHorizontal: 12,
    paddingVertical: 5,
    borderRadius: 8,
    backgroundColor: '#1e293b',
  },
  logBtnText: {color: '#cbd5e1', fontSize: 11, fontWeight: '600'},
  logScroll: {maxHeight: 260, paddingHorizontal: 12, paddingVertical: 8},
  logEmpty: {color: '#475569', fontSize: 11, fontFamily: 'monospace'},
  logLine: {color: '#94a3b8', fontSize: 10, fontFamily: 'monospace', lineHeight: 15},
});

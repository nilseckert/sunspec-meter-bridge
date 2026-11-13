# sunspec-meter-bridge

Macht die Leistungsmessung von [Shelly](https://www.shelly.com)-Geräten und
[go-e](https://go-e.com)-Wallboxen für Fronius-Wechselrichter sichtbar: Der Container emuliert pro
konfiguriertem Gerät einen **Modbus-TCP-Zähler nach SunSpec (Model 213)**, wie ihn der Fronius Smart
Meter TCP bereitstellt. Jedem Zähler werden ein oder drei Kanäle zugeordnet. Im Wechselrichter
(z. B. GEN24, Tauro, Verto) als Sekundärzähler eingebunden, erscheinen die Verbraucher anschließend in
**Fronius Solar.web**.

```
Shelly / go-e (HTTP) ──► sunspec-meter-bridge ──► Modbus TCP :502 ──► Fronius-Wechselrichter ──► Solar.web
```

> Früher `shelly-fronius-meter`. Das Image unter dem alten Namen wird nicht mehr aktualisiert.

> ⚠️ Inoffizielles Community-Projekt – siehe [Haftungsausschluss](#haftungsausschluss).

## Funktionen

- Beliebig viele emulierte Zähler, je mit 1 Kanal (auf L1/L2/L3) oder 3 Kanälen (L1–L3)
- Shelly Gen1 und Gen2+ (Plus/Pro/Mini), Relais mit Leistungsmessung und Energiemesser
- go-eCharger V3/V4 (Gemini, HOME) über die lokale HTTP-API v2, ein- oder dreiphasig
- Leistung, Spannung, Strom, Leistungsfaktor, Frequenz sowie Energiezähler Bezug/Einspeisung
- Zählerstände laufen nie rückwärts (Shelly-Reset/-Tausch) und werden persistent gespeichert
- Mehrere Zähler auf einem Port (Unterscheidung per Modbus-Adresse) oder auf eigenen Ports
- Bei veralteten Messwerten antwortet der Zähler mit einer Modbus-Exception statt alte Werte zu liefern
- Kleines Multi-Arch-Image (amd64, arm64, arm/v7), läuft als Non-Root, mit Healthcheck

## Schnellstart mit Docker Compose

```bash
curl -O https://raw.githubusercontent.com/nilseckert/sunspec-meter-bridge/main/compose.yaml
curl -o config.yaml https://raw.githubusercontent.com/nilseckert/sunspec-meter-bridge/main/config.example.yaml
# config.yaml anpassen
docker compose up -d
docker compose logs -f
```

Siehe [compose.yaml](compose.yaml). Ohne Compose:

```bash
docker run -d --name sunspec-meter-bridge --restart unless-stopped \
  -p 502:502 -p 8080:8080 \
  -v $PWD/config.yaml:/config/config.yaml:ro -v sunspec-meter-bridge:/data \
  ghcr.io/nilseckert/sunspec-meter-bridge:latest
```

## Konfiguration

Vollständiges Beispiel: [config.example.yaml](config.example.yaml). Umgebungsvariablen der Form
`${VAR}` werden in der Config ersetzt.

### Global

| Schlüssel        | Default | Beschreibung                                                              |
|------------------|---------|---------------------------------------------------------------------------|
| `listen_port`    | `502`   | Modbus-Port für Geräte ohne eigenen `port`                                |
| `poll_interval`  | `2s`    | Abfrageintervall der Shellys                                              |
| `http_timeout`   | `2s`    | Timeout je Shelly-Request                                                 |
| `stale_after`    | `30s`   | Ältere Messwerte → Modbus-Exception 0x0B                                  |
| `state_file`     | leer    | JSON-Datei für die Zählerstände, z. B. `/data/state.json` (leer = aus)    |
| `state_interval` | `1m`    | Speicherintervall der Zählerstände (zusätzlich beim Beenden)              |

### Gerät (`devices[]`)

| Schlüssel      | Default                | Beschreibung                                                     |
|----------------|------------------------|------------------------------------------------------------------|
| `name`         | `device-N`             | Name für Logs                                                    |
| `port`         | `listen_port`          | Modbus-TCP-Port                                                  |
| `unit_id`      | `1`                    | Modbus-Adresse, je Port eindeutig                                |
| `serial`       | aus Port/Unit-ID       | Seriennummer, **muss eindeutig sein** (Solar.web, Zählerstände)  |
| `phase`        | `L1`                   | Phase bei genau einem Kanal                                      |
| `manufacturer` | `Fronius`              | SunSpec-Herstellerkennung                                        |
| `model`        | `Smart Meter TS 65A-3` | SunSpec-Modellkennung                                            |
| `channels`     | –                      | 1 oder 3 Kanäle                                                  |

### Kanal (`channels[]`)

| Schlüssel  | Beschreibung                                                     |
|------------|------------------------------------------------------------------|
| `host`     | IP oder Hostname des Geräts (optional mit `:port`)               |
| `type`     | siehe Tabelle unten                                              |
| `id`       | Kanal-/Komponenten-ID am Shelly (Default `0`)                    |
| `phase`    | Phase an der Quelle: `a`/`b`/`c`, `1`/`2`/`3` oder `L1`/`L2`/`L3`. Pflicht bei `shelly_em`, optional bei `goe` (`em_phase` geht weiterhin) |
| `invert`   | Vorzeichen umkehren (Bezug ↔ Einspeisung)                        |

| `type`               | Geräte (Beispiele)                       | Abfrage                              |
|----------------------|------------------------------------------|--------------------------------------|
| `shelly_switch`      | Plus/Pro 1PM, 2PM, 4PM, Plug S (Gen2+)   | `Switch.GetStatus`                   |
| `shelly_pm1`         | Plus PM Mini, PM Mini Gen3               | `PM1.GetStatus`                      |
| `shelly_em1`         | Pro EM-50, Pro 3EM (Profil Monophase)    | `EM1.GetStatus`, `EM1Data.GetStatus` |
| `shelly_em`          | Pro 3EM (Profil Triphase)                | `EM.GetStatus`, `EMData.GetStatus`   |
| `shelly_gen1_relay`  | Shelly 1PM, 2.5, Plug (Gen1)             | `/status` → `meters[id]`             |
| `shelly_gen1_emeter` | Shelly EM, 3EM (Gen1)                    | `/status` → `emeters[id]`            |
| `goe`                | go-eCharger V3/V4 (Gemini, HOME)         | `/api/status?filter=nrg,eto`         |

Die Shelly-Typnamen entsprechen den Komponenten der Shelly-API. Die Namen ohne `shelly_`-Präfix aus
v0.1/v0.2 (`switch`, `pm1`, `em1`, `em`, `gen1_relay`, `gen1_emeter`) funktionieren weiterhin.

Positive Leistung bedeutet Verbrauch. Geräte mit aktivierter Authentifizierung werden derzeit nicht
unterstützt.

### go-e Wallboxen

In der go-e App muss unter *Internet → Erweiterte Einstellungen* die **lokale HTTP-API v2**
aktiviert sein. Zwei Varianten:

```yaml
# dreiphasig: L1–L3 einzeln (empfohlen)
- name: Wallbox Carport
  unit_id: 14
  channels:
    - { host: go-echarger_123456.local, type: goe, phase: 1 }
    - { host: go-echarger_123456.local, type: goe, phase: 2 }
    - { host: go-echarger_123456.local, type: goe, phase: 3 }

# einphasig gemeldet: Summe aller Phasen auf L1
- name: Wallbox Zaun
  unit_id: 15
  channels:
    - { host: go-echarger_654321.local, type: goe }
```

Die Wallbox hat nur einen Gesamtzähler (`eto`). Bei drei Kanälen wird er zu je einem Drittel auf die
Phasen verteilt, die Summe in Solar.web stimmt. Pro Abfrage wird die Wallbox nur einmal angefragt.

## Einrichtung im Fronius-Wechselrichter

1. Weboberfläche des Wechselrichters öffnen → *Gerätekonfiguration* → *Komponenten*.
2. *Komponente hinzufügen* → Zähler, Typ *Fronius Smart Meter TCP* (bzw. Modbus-TCP-Zähler).
3. IP-Adresse des Docker-Hosts, Port und Modbus-Adresse (= `unit_id`) eintragen.
4. Position *Verbraucherzweig* (Sekundärzähler) wählen und speichern.

Die Bezeichnungen können je nach Wechselrichter und Firmware abweichen. Der Container sollte eine feste
IP-Adresse haben.

### Mehrere Zähler: eine IP pro Zähler

Der Wechselrichter akzeptiert **pro IP-Adresse nur einen virtuellen Zähler**, auch wenn sich die
Modbus-Adressen unterscheiden. Für mehrere Zähler läuft deshalb je eine Instanz mit eigener LAN-IP,
z. B. per macvlan: siehe [compose.macvlan.yaml](compose.macvlan.yaml). Jede Instanz bekommt eine eigene
Config mit genau einem Gerät und ein eigenes Datenverzeichnis. Als Port sind im Wechselrichter nur 502
und 1502 wählbar. Die Modbus-Adressen 1–3 können am RTU-Bus des Wechselrichters bereits belegt sein;
höhere Adressen (z. B. ab 11) vermeiden Konflikte.

## Betrieb

- **Logs:** `LOG_LEVEL=debug` zeigt Modbus-Zugriffe und jede Shelly-Abfrage.
- **Health:** `GET :8080/healthz` liefert 200, solange alle Zähler aktuelle Werte haben
  (`HEALTH_ADDR` ändert die Adresse). Das Image enthält einen Docker-`HEALTHCHECK`.
- **Zählerstände:** werden mit `state_file` atomar gespeichert. Schlüssel ist die `serial` –
  wird sie geändert, beginnt der Zähler wieder beim Stand des Shellys. Das Verzeichnis muss für
  UID 65532 schreibbar sein (in Kubernetes z. B. `fsGroup: 65532`).

### Registerbelegung

| Register    | Inhalt                                              |
|-------------|-----------------------------------------------------|
| 40000       | `SunS`                                              |
| 40002–40068 | Common Model (ID 1): Hersteller, Modell, Seriennr.  |
| 40069–40194 | Meter Model 213 (Drehstrom, float32)                |
| 40195       | End Model (`0xFFFF`)                                |

Unterstützt werden die Funktionscodes 3 und 4 (Register lesen).

## Docker-Images

Images werden per GitHub Actions nach `ghcr.io/nilseckert/sunspec-meter-bridge` gebaut:

| Auslöser                | Tags                    |
|-------------------------|-------------------------|
| Git-Tag, z. B. `v1.0.0` | `v1.0.0`, `latest`      |
| Branch `feature/<name>` | `feature_<name>`        |
| `main`, Pull Requests   | nur Test und Build      |

## Entwicklung

```bash
go test ./...
go run . -config config.yaml
docker build -t sunspec-meter-bridge .
```

## Haftungsausschluss

Dieses Projekt ist ein privates, inoffizielles Open-Source-Projekt. Es steht in **keiner Verbindung**
zur Fronius International GmbH, zur Shelly Group (Allterco Robotics) oder zur go-e GmbH und wird von
diesen weder unterstützt noch geprüft. „Fronius“, „Solar.web“, „Shelly“ und „go-e“ sind Marken der jeweiligen Inhaber und
werden hier nur zur Beschreibung der Kompatibilität genannt.

Die Software wird **ohne jede Gewährleistung** bereitgestellt (siehe [LICENSE](LICENSE)). Die Nutzung
erfolgt **auf eigenes Risiko**. Insbesondere gilt:

- Die emulierten Zähler sind **keine geeichten Messgeräte**. Die Werte eignen sich nicht für
  Abrechnungszwecke.
- Falsche oder fehlende Messwerte können das Verhalten des Wechselrichters beeinflussen, z. B.
  Eigenverbrauchsoptimierung, Batteriesteuerung, Einspeisebegrenzung oder Lastmanagement. Die
  Konfiguration ist vor dem produktiven Einsatz zu prüfen. Der Primärzähler am Netzanschlusspunkt
  sollte **nicht** durch diese Software ersetzt werden.
- Firmware-Updates von Fronius oder Shelly können die Kompatibilität jederzeit beenden.
- Arbeiten an elektrischen Anlagen (z. B. Einbau von Shellys) dürfen nur von qualifizierten
  Elektrofachkräften durchgeführt werden.
- Der Modbus-Port bietet keine Authentifizierung und darf nicht aus dem Internet erreichbar sein.

Die Autoren haften nicht für Schäden an Geräten, Anlagen, Daten oder für entgangene Einspeisevergütung
und sonstige Folgeschäden, soweit gesetzlich zulässig.

## Lizenz

[MIT](LICENSE) © 2026 Nils Eckert

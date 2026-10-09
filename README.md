# EBS Bankacılık ve Sigortacılık Eğitim Simülasyonu

Bu depo, 09.10.2026 tarihli yeniden geliştirme kılavuzundaki kuralları temel alan, kurulumsuz bir geliştirme çekirdeğidir. Merkezi API ve SQLite veri deposu aynı gerçek veriyi Admin, Personel ve Müşteri görünümleri için kullanır.

## Çalıştırma

Python 3.11+ yeterlidir; harici paket gerekmez.

```bash
cd /workspace/EBS
python3 -m ebs.server --host 127.0.0.1 --port 8792
```

Tarayıcıdan `http://127.0.0.1:8792/` adresini açın. İlk açılışta `var/ebs.sqlite3` oluşturulur ve 30 adet `SCENARIO_DEMO` müşteri, 60 hesap, kart/kredi/poliçe/hasar kayıtları ile kampanyalar eklenir. Uygulama EBS_02 v3.1 sözleşmesindeki merkezi 8792 giriş modelini kullanır; eski 8787 çekirdeği bu dikey dilim için gerekli değildir.

Temel testler:

```bash
python3 -m unittest discover -s tests -v
./scripts/smoke_test.sh
```

Varsayılan eğitim kullanıcıları v3.1 kimlikleriyle seed edilir: `admin / EBS2026!`; `gise01`, `gise02`, `musteritemsilci`, `operasyonyetkilisi`, `krediyetkilisi`, `sigortayetkilisi`, `hasaryetkilisi`, `denetci` / `1234`. Müşteri demo kullanıcı adı müşterinin tam adı, şifresi `1234` şeklindedir. Giriş sonrası Bearer oturumu kullanılır; state değiştiren uçlar oturum olmadan çalışmaz.

## Gerçek işlem akışları

- Kasa açma ve nakit yatırma hesap ile kasa bakiyesini aynı SQLite transaction içinde değiştirir.
- Transferlerde kaynak sahipliği, hesap durumu, para birimi, bakiye ve idempotency anahtarı doğrulanır. Yüksek tutar işlemi bağımsız onay için merkezi iş havuzuna düşer.
- Her parasal işlem dengeli çift taraflı jurnal, işlem referansı, correlation ID, audit kaydı ve gerekiyorsa eğitim ibareli PDF dekont üretir.
- Demo sıfırlama endpoint'i tüm seed verisini güvenli biçimde yeniden oluşturur.

## v3.1 uyumluluk uçları

- `GET /api/health`, `GET /health`, `GET /runtime.json`
- `POST /api/login`, `GET /api/session`, `POST /api/logout`
- `GET /api/catalog`, `GET /api/card/<kod>`, `GET /api/events`, `POST /api/action`
- `GET /api/customer-portal/summary` müşteri oturumunu kendi müşteri kaydıyla sınırlar.

`/api/manifest` ve `/api/catalog` aynı 206 kart sözleşmesini sunar. Aksiyonlar benzersiz referans, kullanıcı, istemci, durum ve idempotency anahtarıyla SQLite'a kaydedilir.

## Kapsam ve kaynak kısıtı

`manifest/cards.json`, verilen 206 ekran sözleşmesindeki kartları gerçek uygulama türü ve API sahibiyle eşler. Ana kılavuz ve devir notları `docs/` altında tutulur.

EBS_01 eski çalışan davranış ve ekran referansı, EBS_02 v3.1 merkezi Server kaynak tabanı, EBS_03 ise kurulabilir Windows paket referansı olarak incelendi. Bu Linux çalışma alanında Windows EXE'leri çalıştırmak yerine aynı merkezi API sözleşmesini test edilebilir Python/SQLite çekirdeğinde uyguladık. EBS_03 içindeki payload SHA256 kayıtları doğrulandı; yalnızca `SHA256SUMS.txt` dosyasının kendi kendine yazılmış özet satırı tutarsızdır.

Windows UAC, servis, firewall, LAN, kısayol, uninstall/restore/upgrade ve üç ayrı EXE fiziksel testleri bu Linux ortamında yapılmadı; başarılı kabul edilmiş gibi raporlanmamalıdır.

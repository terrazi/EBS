# EBS Bankacılık ve Sigortacılık Eğitim Simülasyonu

Windows kurulum paketindeki merkezi Server ve Admin, Personel, Müşteri istemcilerinin kaynakları `v31/` altındadır. Merkezi Go API ve SQLite veri deposu aynı veriyi üç rol için sunar. Windows kurulumu [kurulum kılavuzunda](docs/WINDOWS_KURULUM.txt) açıklanır.

## Çalıştırma

Güncel merkezi Server için Go 1.23.2 kullanılır. Linux geliştirme ortamında da Windows paketiyle aynı API ve işlem motoru çalıştırılır:

```bash
cd /workspace/EBS/v31/server
go run .
```

Merkezi Server 8792 portunu kullanır; `/health` protokol, derleme sürümü ve işlem motorunun hazır olma durumunu döndürür. İlk açılışta SQLite eğitim veritabanı oluşturulur. Windows'ta veri konumu `C:\ProgramData\EBS Bankacilik ve Sigortacilik\v3pro`; Linux'ta `ProgramData` ortam değişkeniyle veri kökü seçilebilir. Eski 8787 çekirdeği giriş için gerekli değildir.

Temel testler:

```bash
python3 -m unittest discover -s tests -v
./scripts/smoke_test.sh
cd v31/server && go test ./...
```

Varsayılan eğitim kullanıcıları v3.1 kimlikleriyle seed edilir: `admin / EBS2026!`; `gise01`, `gise02`, `musteritemsilci`, `operasyonyetkilisi`, `krediyetkilisi`, `sigortayetkilisi`, `hasaryetkilisi`, `denetci` / `1234`. Müşteri demo kullanıcı adı müşterinin tam adı, şifresi `1234` şeklindedir. Giriş sonrası Bearer oturumu kullanılır; state değiştiren uçlar oturum olmadan çalışmaz.

## Gerçek işlem akışları

- Kasa açma ve nakit yatırma hesap ile kasa bakiyesini aynı SQLite transaction içinde değiştirir.
- Transferlerde kaynak sahipliği, hesap durumu, para birimi, bakiye ve idempotency anahtarı doğrulanır. Yüksek tutar işlemi bağımsız onay için merkezi iş havuzuna düşer.
- Her parasal işlem dengeli çift taraflı jurnal, işlem referansı, correlation ID, audit kaydı ve gerekiyorsa eğitim ibareli PDF dekont üretir.
- Demo sıfırlama endpoint'i tüm seed verisini güvenli biçimde yeniden oluşturur.

## v3.1 uyumluluk uçları

- `GET /health`, `GET /v3api/api/health`, `GET /runtime.json`
- `POST /api/login`, `GET /api/me`, `POST /api/logout`
- `GET /v3api/api/catalog`, `GET /v3api/api/card/<kod>`, `GET /v3api/api/events`, `POST /v3api/api/action`
- `GET /api/customer-portal/summary` müşteri oturumunu kendi müşteri kaydıyla sınırlar.

`/api/manifest` ve `/api/catalog` aynı 206 kart sözleşmesini sunar. Aksiyonlar benzersiz referans, kullanıcı, istemci, durum ve idempotency anahtarıyla SQLite'a kaydedilir.

## Kapsam ve kaynak kısıtı

`manifest/cards.json`, verilen 206 ekran sözleşmesindeki kartları gerçek uygulama türü ve API sahibiyle eşler. Ana kılavuz ve devir notları `docs/` altında tutulur.

EBS_01 eski çalışan davranış ve ekran referansı, EBS_02 v3.1 merkezi Server kaynak tabanı, EBS_03 ise kurulabilir Windows paket referansıdır. `ebs/` altındaki Python uygulaması ayrı geliştirme prototipidir; Windows installer payload'u değildir. Kurulum ve istemci bağlantı testleri `v31/` altındaki gerçek Go Server üzerinde yürütülmelidir.

Windows paketini tek kaynak sürümünden yeniden üretmek için `scripts/build_windows_release.py build --output /tmp/ebs-release --go <go-yolu>` çalıştırılır. EXE doğrulaması ve kaynak/payload commit'inden sonra `package` komutu aynı klasörü kullanır. Kaynak veya gömülü dosya özetleri değişmişse paketleme durdurulur. Derleme kimliği, kaynak commit'i ve SHA256 özetleri iki ZIP'teki `BUILD_MANIFEST.json` dosyasında bulunur.

Windows EXE'leri için Wine üzerinde ek çalışma kontrolleri yapılabilir; bu kontroller gerçek Windows UAC, Defender, firewall ve sınıf ağı kabul testinin yerini tutmaz. Doğrulama sonuçları [11 Ekim kontrol raporunda](docs/REVIZYON_DOGRULAMA_20261011.md) belirtilir. Bu sürüm kurulum ve giriş düzeltmesidir; 206 kartın tümünün kabulünü veya hasar modülünün tamamlandığını ifade etmez.

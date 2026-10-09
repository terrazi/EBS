# Uygulama kapsamı ve kaynak önceliği

Bağlayıcı talimatlar, kullanıcı isteğinden ayrı olarak, ekli kılavuz ve revizyon belgesindeki üretim kurallarıdır. Kaynak önceliği şöyledir:

1. v3.1 STABLE kurulum/Server keşfi/giriş bağlantısı (EBS_02) teknik tabandır.
2. Eski çalışan EBS (EBS_01) davranış ve kullanıcı deneyimi referansıdır.
3. 206 ekran sözleşmesi kapsam ve kart eşlemesini belirler.
4. Yeniden geliştirme kılavuzu çatışma çözümünü ve kabul ölçütünü belirler.

Bu ilk dikey dilim kılavuzun işlem motoru kurallarını çalışır biçimde uygular:

- SQLite üzerinde merkezi kalıcılık ve migration-safe seed;
- 30 `SCENARIO_DEMO` müşteri, 60 hesap ve bağlı kart/kredi/poliçe/hasar kayıtları;
- müşteri 360°, kasa açılışı/nakit yatırma ve transfer;
- bakiye ve sahiplik doğrulamaları, idempotency, maker-checker;
- borç = alacak kontrolü, işlem referansı/correlation ID, old→new audit;
- eğitim ibareli PDF dekont ve 206 kartın generic olmayan manifest eşlemesi.

EBS_01 RAR, EBS_02 çok parçalı RAR ve EBS_03 RAR bu çalışma alanında incelendi. EBS_02'nin v3.1 merkezi 8792 Server sözleşmesi (giriş, katalog, aksiyon, olay ve müşteri portalı) Python/SQLite çekirdeğine bağlandı. EBS_03'teki dokuz payload SHA256 değeri doğrulandı; `SHA256SUMS.txt` satırı dosyanın kendi özetini içerdiği için kendisiyle eşleşmiyor.

Bu çalışma alanı Windows EXE derlemesi yerine Linux üzerinde çalıştırılabilen ve aynı merkezi akışları doğrulayan bir geliştirme çekirdeğidir. Windows installer, servis, firewall, LAN ve üç ayrı EXE'nin fiziksel kabul testleri bu ortamda yapılmadı.

Kapsamda olmayan fiziksel doğrulamalar: Windows UAC/Service/Firewall/LAN, üç ayrı EXE installer, uninstall/restore/upgrade ve çözünürlük/ölçek kabul testleri.

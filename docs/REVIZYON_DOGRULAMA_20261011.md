# 11 Ekim 2026 kurulum ve giriş revizyonu

Derleme kimliği: `20261011-startup-fix-1`. Kesin kaynak commit ve EXE SHA256 değerleri iki ZIP içindeki `BUILD_MANIFEST.json` dosyasındadır.

Önceki teslimattaki Windows Server EXE’si Wine üzerinde çalıştırıldı. Sağlık yanıtı `engine_ready=false`, günlük `sqlite3: unable to open database file` gösterdi. Kullanılan SQLite WASM/VFS sürücüsü `file:///C:/...` URI’sini Windows dosya sistemine `/C:/...` olarak iletiyordu. Yeni sürüm `file:C:/...` kullanıyor; boşluk, Türkçe karakter ve UNC yolu gerçek sürücü seviyesinde test ediliyor.

Windows hizmeti başlangıcında SCM önce `START_PENDING` ve ilerleme bildirimi alıyor. SQLite ve HTTP dinleyicisi hazır olduktan sonra `RUNNING` bildiriliyor. Başlangıç hatası servis hata koduyla iletiliyor. Yalnız SCM bağlantısı yoksa (1063) konsol çalışmasına geçiliyor. TCP portu veritabanından önce bağlanıyor; ikinci süreç aynı veritabanını açmadan duruyor.

Sağlık uçları isteğe bağlı eski 8787 servisini beklemiyor. Kurulum ve istemciler aynı protokol/derleme/motor kontrolünü kullanıyor. Motor hatası JSON503 içinden okunup gösteriliyor. Kurucu SCM kaydını silip kısa beklemeye dayanmak yerine durdurmayı teyit ediyor, kaydı güncelliyor ve gerçek hazırlığı bekliyor. Admin istemcisi ısınan hizmeti öldürmüyor veya ikinci konsol sunucusu açmıyor.

Giriş, müşteri/hesap okumaları ve oturum denetimi aynı kalıcı veritabanına bağlandı. v3 girişi başarısızken ayrı örnek kullanıcıyla başarı döndürülmüyor. Uyumluluk ve v3 API’si aynı süreli token’ı kullanıyor. Çıkış iki API’de token’ı geçersiz kılıyor. Müşteri portalı yalnız oturum sahibinin gerçek eğitim kayıtlarını okuyor. Kullanım belgesindeki müşteri isimleri kalıcı seed ile eşleştirildi.

Doğrulama kapsamı:

- Linux Go Server ve işlem motoru testleri; Python prototip testleri ayrıca çalıştırıldı.
- İstemci ve kurucu HTTP testleri: hazır yanıt, eski sürüm, geçersiz JSON, HTTP503 motor hatası, yönlendirme, büyük yanıt ve 1500ms yanıt.
- Gerçek Windows test EXE’leri Wine üzerinde çalıştırıldı: SQLite dosya/VFS ve başlangıç/durma/yeniden açılış testleri.
- Gerçek Windows hizmet EXE’si Wine SCM üzerinden başlatıldı, durduruldu ve aynı veritabanıyla yeniden başlatıldı. Mevcut oturum ve veritabanı işareti korundu.
- Port çakışmasında servis STOPPED, Windows1066/servis1 döndürdü; günlük gerçek bind hatasını kaydetti. Port serbest bırakıldığında yeniden başlangıç geçti.
- 39 seed kullanıcı (1 Admin, 8 Personel, 30 Müşteri) aynı Windows hizmetine giriş yaptı. Yanlış şifre ve uygunsuz istemci rolü JSON401 döndürdü; tüm müşteri hesapları sahiplik kontrolünden geçti.
- ZIP’ler tek derlemeden üretiliyor. Paketleme, derleme sonrasında kaynak veya payload değişmişse duruyor; arşiv bütünlüğü ve yayınlanan dosya SHA256 değerleri denetleniyor.

Wine kontrolleri fiziksel Windows 10/11 UAC, Defender, kısayol, güvenlik duvarı veya ayrı bilgisayarlardan sınıf ağı testinin yerine geçmez. Bu paket kurulum/giriş sorunları için düzeltmedir. Önceki kapsam raporlarında açık kalan 206 kartın tümünün kabulü ve kalıcı hasar işlem motoru tamamlanmış değildir; bu sürüm bunların tamamlandığı beyanını taşımaz.

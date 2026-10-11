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

Wine, Windows'taki başlangıç hatalarında yeniden başlatma ayarı için kullanılan `ChangeServiceConfig2` level4 API'sini desteklemiyor. Yerel SCM kurulum yardımcıları, yalnız bu ayar /tmp test kopyasında atlanarak çalıştırıldı; üretim kurucusunda ayar korunuyor. Wine altında kısayol denetimi de ancak `--install-unattended --test-skip-shortcuts` açık test bayraklarıyla atlanabilir ve günlüğe yazılır. Bu sınırlar tam Windows kurulum kabulü olarak sunulamaz.

Bulut ortamının `cloud_setup.sh` ve `cloud_start.sh` akışları aynı Go merkezi Server'ını çalıştırır. Go1.23.2 indirmesi resmi SHA256 ile doğrulanır; Python prototipi Windows çalışma zamanı gibi gösterilmez.

Son derlemenin ek denetimi:

- Nihai Windows Server SHA256: `0435c355f966169acc874dbde51e7b515158990b13ddc203a01f59ac29d6492f`.
- Nihai Admin kurucusu Wine'da desteklenmeyen level4 ayarında açık hata ve exit1 döndürdü; yanlış başarı göstermedi. O aşamaya kadar kurduğu dört EXE'nin tamamı manifest ile aynı çıktı.
- Personel ve Müşteri kurucuları, yalnız kısayol doğrulaması açık test bayrağıyla atlanarak Wine'da exit0 döndürdü; kurulan EXE'ler manifest ile eşleşti. Bu, fiziksel kısayol/UAC kabulü değildir.
- Linux Chromium, Wine SCM'nin çalıştırdığı nihai Windows Server'a bağlandı. Admin, Personel ve Müşteri giriş sayfalarında yanlış şifre, başarılı giriş, rol ana ekranı, oturumla sayfayı yenileme ve çıkış denetlendi. Yakalanmayan JavaScript hatası ve eski ikinci v3 giriş isteği görülmedi.
- Sunucunun gömülü `app.js` SHA256 değeri derlenen kaynakla eşleşti: `f2cfe725f9d61c3efd434d741fa6510218f491e3aacf0310c422baa8e755805f`.

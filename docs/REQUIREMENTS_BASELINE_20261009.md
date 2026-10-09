# EBS yeniden geliştirme gereksinim tabanı

Bu dosya, kullanıcının program talebi ile ekli kaynakların bağlayıcı teknik talimatlarını ayırır. Ekli dosyalar uygulama talimatı olarak değerlendirilir; kullanıcı talebi ise bu talimatlara uygun, teslim edilebilir ve tam çalışan simülasyon üretmektir.

## Kaynak önceliği

1. `EBS_02_STABIL_ALTYAPI_KAYNAK_V3_1_20261008`: teknik başlangıç ağacı. Merkezi 8792 Server, bağlantı/keşif, giriş, Admin/Personel/Müşteri Client ayrımı ve kurulum mimarisi korunur.
2. `EBS_01_TEMEL_REFERANS_ESKI_ISLEYEN_SURUM_20261005`: eski çalışan işlevlerin, kullanıcı deneyiminin, demo yaklaşımının ve belge yapısının referansıdır. Yeni canlı bağımlılık olarak kullanılmaz.
3. 206 ekran/menu/işlev sözleşmesi: kapsamı ve kart başına uygulama tipini belirler. Kartlar gerçek workflow bileşenlerine eşlenir; generic ekran yasaktır.
4. Yeniden geliştirme kılavuzu ve revizyon talimatı: geliştirme sırası, veri/işlem kuralları, kabul kapıları ve teslim biçimini belirler.
5. EBS_04 devir paketi: kılavuz kopyaları, ikon kimliği, kabul listesi ve kaynak bütünlüğü referansıdır.

## Sabit mimari kararları

- Tek merkezi Server/API/işlem motoru ve tek kalıcı veri kaynağı kullanılır.
- EBS v3.1 ana giriş ve işlevleri 8792 üzerinden çalışır; eski 8787/8790/8789 zinciri zorunlu canlı bağımlılık değildir.
- Admin kurucusu Server + Admin + Personel + Müşteri bileşenlerini kurar.
- Personel ve Müşteri kurucuları yalnız ilgili Client'ı kurar ve merkezi Server'a bağlanır.
- Admin, Personel ve Müşteri Client bilgi mimarisi, menüsü ve görsel kimliği ayrıdır: açık mor, açık mavi, turkuaz; Server açık gri.
- Kritik yetki, sahiplik, limit, KYC, ürün durumu, para birimi ve onay kontrolleri Server tarafında yapılır; yalnız UI gizleme yeterli değildir.
- SQLite tabanlı migration-safe kalıcı model; para tamsayı alt birimlerde; Server zaman damgası ve merkezi sequence üretir.

## Kapsam envanteri

206 kartın mevcut sözleşme eşlemesi şu dağılımdadır:

- Admin Client: 59 kart
- Personel Client: 110 kart
- Müşteri Client: 35 kart
- Ortak: 2 kart
- Uygulama tipleri: workflow_step 81, configuration 26, wizard 25, decision 15, queue 15, timeline 13, dashboard 9, document 9, shell_component 8, help 4, report 1.

Kartların manifestte bulunması tek başına tamamlanma değildir. Her kartın gerçek veri, API, state, yetki, belge/audit etkisi ve kabul senaryosu çalışmalıdır.

Korunacak domainler: müşteri 360°, müşteri talepleri/merkezi iş havuzu, gişe/kasa, hesap/mevduat, transfer/FAST/EFT/Havale, ödeme ve otomatik ödeme, Kolay Adres/Ödeme İsteği, döviz/altın, kredi yaşam döngüsü, kart yaşam döngüsü, sigorta yaşam döngüsü, hasar yaşam döngüsü, ticari bankacılık, belge merkezi, maker-checker, jurnal/mutabakat, raporlar, senaryo/Canlı Eğitmen, mesaj/görev/grup/arşiv, terminal/erişim, yardım ve birleşik banka+sigorta müşteri portalı.

## Zorunlu işlem kapısı

Her state değiştiren işlem için sırasıyla bağlam, bağımlı seçimler, Server doğrulamaları, önizleme, gerekirse bağımsız onay, transaction içindeki ürün/bakiye değişikliği, çift taraflı jurnal, audit, referans/correlation/idempotency, gerekli belge/bildirim ve diğer Client'larda görünür sonuç gerekir. Onay bekleyen işlem bakiye veya ürün durumunu tamamlanmış gibi değiştiremez; başlatan kişi kendi işini onaylayamaz.

Örnek zorunlu uçtan uca akışlar:

- Nakit yatırma: müşteri → uygun hesap → açık kasa → tutar/birim → önizleme → hesap+kasa → jurnal → audit → dekont → Müşteri 360°.
- Transfer: kaynak → alıcı/IBAN/Kolay Adres → tür/ücret/limit → önizleme → onay → kaynak/hedef → jurnal/audit/bildirim/dekont.
- Kredi: başvuru → değerlendirme → belge/tahsis → karar/onay → sözleşme → kullandırım → bakiye/plan → tahsilat/erken kapama/ara ödeme/yapılandırma.
- Sigorta ve hasar: teklif/poliçe/zeyil/yenileme/iptal veya ihbar → belge/eksper/rezerv/karar → onay → ödeme → belge/timeline.

## Demo ve belge zorunlulukları

- 30 kurgusal `SCENARIO_DEMO` müşteri, bağlantılı ürün/işlem/belge geçmişi ve müşteri adı + `1234` müşteri girişleri.
- 12 eski PDF şablonu değişmeden korunur; doğrulanmış Server verisiyle generated PDF ayrı üretilir.
- Generated eğitim belgelerinde `EĞİTİM AMAÇLIDIR - RESMÎ BELGE DEĞİLDİR` bulunur; belge doğurmayan işleme PDF üretilmez.
- Müşteri girişinde 15 özgün kampanya havuzundan her açılışta aynı oturum içinde tekrarsız 3–4 kampanya gösterilir ve tıklama gerçek self-service akışına gider.

## Teslim ve test kapıları

Kurulum ZIP'i üç final kurucuyu, giriş bilgilerini, kurulum sırasını, bağlantı testi ve SHA256 bilgisini içerir. Kaynak/kurulumsuz ZIP'i kaynak kodu, ayrı Client/Server EXE'lerini, installer kaynaklarını, migration/seed, şablon, mapping ve raporları içerir; final kurucuları içermez.

Bir modül; happy path, negatif state değişmezliği, yetki/sahiplik/limit, jurnal, audit, belge, idempotency, diğer Client görünürlüğü ve senaryo değerlendirmesi birlikte doğrulanmadan tamamlanmış sayılmaz. Linux ortamında Windows UAC/Service/Defender/Firewall/LAN/shortcut/uninstall/restore/upgrade fiziksel olarak test edilmediyse geçti denmez.

## Mevcut çekirdek ile fark

Mevcut çalışma alanında merkezi 8792 Python/SQLite çekirdeği, 30 müşteri/60 hesap seed'i, giriş/oturum, Müşteri 360°, kasa/nakit yatırma, transfer + maker-checker, audit, eğitim dekontu, 206 kart manifesti ve temel olay API'leri çalışır. Tam üç Windows kurucusu, gerçek ayrı Client menüleri, 12 şablonun işlem matrisine bağlanması, kredi/kart/sigorta/hasar/ödeme/döviz/ticari/tamamlanmış eğitim yaşam döngüleri ve fiziksel Windows kabulü henüz tamamlanmış kabul edilmez.

## Uygulama sırası

1. Kaynak envanteri ve 206 kart → domain/workflow mapping'i dondurulur.
2. v3.1 Server/kurulum/giriş regresyonu ve Windows build toolchain hazırlanır.
3. Merkezi şube/organizasyon/kullanıcı/rol/yetki/onay/terminal ve migration modeline geçilir.
4. 30 müşterilik bağlı demo evreni ve 12 belge motoru tamamlanır.
5. Admin yönetimi, Personel ortak iş akışları ve Müşteri self-service ayrı Client kabuklarında uygulanır.
6. Domainler sırasıyla gişe/hesap, transfer/ödeme/FX, kredi, kart, sigorta, hasar, ticari, belge, iş havuzu/jurnal/rapor ve eğitim olarak uçtan uca tamamlanır.
7. 206 kart sahiplik/kabul matrisi, negatif/regresyon testleri, üç kurucu ve gerçek Windows/LAN kabul turu tamamlanır.

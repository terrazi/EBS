# EBS uygulama denetimi — 10 Ekim 2026

Bu kayıt, teslimatı tamamlanmış göstermemek için mevcut kod ile V3.1 sözleşmesi arasındaki farkları kanıtlar. Kaynak sözleşme `manifest/stable_design_cards.json` içinde saklanır; bu dosya EBS_02 V3.1 kaynağındaki 206 kartın 18 alanlı sürümüdür.

## Doğrulananlar

- 206 kart kodu ve istemci dağılımı korunuyor: Admin 59, Personel 110, Müşteri 35, Ortak 2.
- Demo çekirdeğinde kasa açma, para yatırma, transferde bağımsız onay, dengeli yevmiye, audit, idempotency ve PDF dekont akışları test ediliyor.
- Mevcut otomatik test seti Python çekirdeğinde 10, Go v3.1 simülasyon motorunda 11 testten oluşuyor ve geçiyor.

## Teslimatı engelleyen açıklar

- `server.py` yalnızca birkaç gerçek akışa sahip; sözleşmedeki kart API aileleri ve `/api/workflows/*` uygulanmış değil.
- `record_action` yalnız `api_events` kaydı oluşturuyor. Bu, kartı başarılı göstermeye yetmez; gerçek domain state, işlem referansı, yevmiye, belge matrisi, bildirim ve negatif kurallar uygulanmalı.
- Kart manifesti artık V3.1 alanlarını taşısa da her kart için gerçek endpoint, tablo, izin, belge, state transition ve kabul testi henüz bağlanmış değil.
- Müşteri sahipliği, rol/departman/limit kontrolleri ve tüm okuma uçlarında oturum zorunluluğu tamamlanmalı.
- 12 değişmez belge şablonunun alanları için sanal belge üretme, doğrulama, reddetme, silme ve işleme bağlama akışları v3 motoruna eklendi; bütün 206 kartın belge matrisi hâlâ tamamlanmış değildir.
- V3.1 payload dizininde gerçek istemci/sunucu EXE'leri bulunuyor ve yeniden üretilebilir; bunlar Windows amd64 cross-build çıktılarıdır. Fiziksel Windows kurulum kabulü yapılmadan "Windows'ta doğrulandı" denemez.
- Windows UAC, servis, Defender/firewall, LAN keşfi, kısayol, kaldırma, yedekleme/geri yükleme ve yükseltme testleri yapılmadı.

Bu dosya bir tamamlanma beyanı değildir; her madde kapatılmadan nihai ZIP'ler bitmiş ürün olarak yayımlanmayacaktır.

## 10 Ekim ilerleme kaydı

- V3.1 Go kaynak ağacı ve payload kaynakları `v31/` altında korunuyor.
- `internal/sim` içinde SQLite işlem motoru, salted demo oturumları, sahiplik kontrolleri, kasa/hesap/transfer/ödeme/FX/otomatik ödeme akışları ile kredi/kart/sigorta/belge vertical slice'ları eklendi.
- Yeni istemci API'si `/v3api/api/v3/login`, `/v3api/api/v3/session`, `/v3api/api/v3/operation` ve `/v3api/api/v3/query` oturum ve transaction sınırlarıyla bağlandı.
- Sanal eğitim belgeleri başarılı işlem sonrasında `ProgramData` altındaki `documents/` dizinine eğitim ibareli PDF olarak materialize ediliyor; sigorta teklif/kredi/kart başvurularında uygun belge ID'si olmadan işlem başlatılmıyor.
- Tüm yeni v3 listelerinde sütun başlığı tıklaması artan/azalan sıralamayı, Ctrl+tıklama ise 1-2-3 şeklinde çok ölçütlü hiyerarşik sıralamayı güncelliyor.
- Go Linux testleri ve Windows amd64 cross-build doğrulandı; fiziksel Windows çalıştırma/UAC/SCM/LAN kabulü hâlâ yapılmadı.
- 206 kartın tamamı bu API'ye bağlanmış değildir; kart bazlı uygulama ve UI kabulleri devam ediyor.

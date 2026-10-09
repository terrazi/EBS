# EBS uygulama denetimi — 10 Ekim 2026

Bu kayıt, teslimatı tamamlanmış göstermemek için mevcut kod ile V3.1 sözleşmesi arasındaki farkları kanıtlar. Kaynak sözleşme `manifest/stable_design_cards.json` içinde saklanır; bu dosya EBS_02 V3.1 kaynağındaki 206 kartın 18 alanlı sürümüdür.

## Doğrulananlar

- 206 kart kodu ve istemci dağılımı korunuyor: Admin 59, Personel 110, Müşteri 35, Ortak 2.
- Demo çekirdeğinde kasa açma, para yatırma, transferde bağımsız onay, dengeli yevmiye, audit, idempotency ve PDF dekont akışları test ediliyor.
- Mevcut otomatik test seti 7 testten oluşuyor ve geçiyor.

## Teslimatı engelleyen açıklar

- `server.py` yalnızca birkaç gerçek akışa sahip; sözleşmedeki kart API aileleri ve `/api/workflows/*` uygulanmış değil.
- `record_action` yalnız `api_events` kaydı oluşturuyor. Bu, kartı başarılı göstermeye yetmez; gerçek domain state, işlem referansı, yevmiye, belge matrisi, bildirim ve negatif kurallar uygulanmalı.
- Kart manifesti artık V3.1 alanlarını taşısa da her kart için gerçek endpoint, tablo, izin, belge, state transition ve kabul testi henüz bağlanmış değil.
- Müşteri sahipliği, rol/departman/limit kontrolleri ve tüm okuma uçlarında oturum zorunluluğu tamamlanmalı.
- 12 değişmez belge şablonunun alanları ve orijinal PDF üretim zinciri mevcut çekirdeğe bağlanmış değil.
- EBS_02 Go kurulum kaynağının payload dizini gerçek istemci/sunucu EXE'lerini içermiyor. Bu nedenle yeni, doğrulanmış Windows kurulum EXE'si bu ortamda üretilmiş sayılamaz. Ortamdaki `/usr/bin/go` Go derleyicisi değil.
- Windows UAC, servis, Defender/firewall, LAN keşfi, kısayol, kaldırma, yedekleme/geri yükleme ve yükseltme testleri yapılmadı.

Bu dosya bir tamamlanma beyanı değildir; her madde kapatılmadan nihai ZIP'ler bitmiş ürün olarak yayımlanmayacaktır.

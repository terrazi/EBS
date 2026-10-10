EBS v3.0 PRO Client launcher source

Bu Client artık yerel 127.0.0.1 üzerinde web sunucusu açmaz.
Admin/Personel/Müşteri EXE yalnız merkezi EBS v3 Server'ı bulur ve
http://<SERVER>:8792/ui/?client=<rol> adresini Edge uygulama modunda açar.

Kullanıcı arayüzü kaynakları: ../server/web/
Bu ayrım 127.0.0.1 bağlantıyı reddetti hatasını istemci tarafında yapısal olarak ortadan kaldırmak için yapılmıştır.

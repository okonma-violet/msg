Пролет месседжа от Клиента до гнезда Аппликейшена и обратно:
    1) app server:
        1.1) project/wsconnector/incomingconn.go: создается AppServerMessage (lib project/app/protocol)
        1.2) там же, на AppServerMessage вызывается Read() (который тоже из либы project/app/protocol)
        1.3) там же, на client{} вызывается Handle(AppServerMessage) (это все уже из project/app/appserver/clientsconns.go)
        1.4) в Handle(AppServerMessage) мы действуем по логике:
            msgtype.SettingsReq:    -клиент не указал appID - вернет apps.index (по протоколу)
                                        (файл apps.index перегоняем в json {{"appid": uint16, "appname": string}} (со строки 80 в appserver/main.go))
                                    -клиент указал appID - вернет <имя приложения>.settings (по протоколу)
                                        (файл <имя приложения>.settings тупо читаем, отправляем тупо то, что прочитали (строка 90 в appserver/main.go))
            все остальное:  1) вернет клиенту 9 байт c таймстемпом (первый байт - msgtype.TypeTimestamp)
                            2) перепакует AppServerMessage в AppMessage (lib project/app/protocol) и отправит в приложение
    2)  app (конкретно на примере project/app/chatapp):
        2.1) project/connector/epoll_connector.go: создается AppMessage (lib project/app/protocol)
        2.2) там же, на AppMessage вызывается Read() (который тоже из либы project/app/protocol)
        2.3) там же, вызывается враппер-Handle(AppMessage) из либы project/appservice/appserver.go
        2.4) и уже из того враппера мы вызываем Handle(AppMessage), который мы уже описываем уже сами в приложении, сейчас это в project/app/chatapp/main.go)
        
    3)  
    4)  


Протокол:
    client <--> appserver : 1 byte msgtype, 1 byte reserved, 2 bytes appID, 8 bytes timestamp, 2 bytes headers len, 4 bytes body len, дальше хедеры и тело
    appserver <--> app : 1 byte msgtype, 1 byte reserved, 4 bytes connUID, 8 bytes timestamp, 2 bytes headers len, 4 bytes body len, дальше хедеры и тело

Auth:
(is - identity server)
    1) client шлет пользовательские логин-пароль в is, is возвращает (краткосрочный одноразовый) грант
    2) client шлет грант в app, app шлет грант + is_appid + is_app_secret(id и secret, которые is выдал app'у) в is
    3) is шлет токены + client_id app'у в ответ, app шлет токен клиенту
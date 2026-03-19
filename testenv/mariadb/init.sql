-- Icinga2 IDO database
CREATE DATABASE IF NOT EXISTS icinga2_ido CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'icinga2_ido'@'%' IDENTIFIED BY 'icinga2_idopass';
GRANT ALL ON icinga2_ido.* TO 'icinga2_ido'@'%';

-- Icinga Web 2 database
CREATE DATABASE IF NOT EXISTS icingaweb2 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'icingaweb2'@'%' IDENTIFIED BY 'icingaweb2pass';
GRANT ALL ON icingaweb2.* TO 'icingaweb2'@'%';

-- Icinga Director database
CREATE DATABASE IF NOT EXISTS director CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'director'@'%' IDENTIFIED BY 'directorpass';
GRANT ALL ON director.* TO 'director'@'%';

FLUSH PRIVILEGES;

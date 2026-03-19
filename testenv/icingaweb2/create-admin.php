<?php
// Create admin user in icingaweb2 database
// Retries until the icingaweb_user table is available

$maxRetries = 60;
$host = 'mariadb';
$db = 'icingaweb2';
$user = 'icingaweb2';
$pass = 'icingaweb2pass';

$hash = password_hash('admin', PASSWORD_BCRYPT);

for ($i = 0; $i < $maxRetries; $i++) {
    try {
        $pdo = new PDO("mysql:host=$host;dbname=$db;charset=utf8mb4", $user, $pass);
        $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);

        $stmt = $pdo->prepare(
            "INSERT INTO icingaweb_user (name, active, password_hash) VALUES (:name, 1, :hash)
             ON DUPLICATE KEY UPDATE password_hash = :hash2"
        );
        $stmt->execute(['name' => 'admin', 'hash' => $hash, 'hash2' => $hash]);

        echo "Admin user ensured (admin/admin)\n";
        exit(0);
    } catch (PDOException $e) {
        // Table doesn't exist yet, retry
        sleep(1);
    }
}

echo "WARNING: Failed to create admin user after {$maxRetries} attempts\n";
exit(1);

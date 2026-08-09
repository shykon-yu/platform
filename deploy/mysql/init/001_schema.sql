SET NAMES utf8mb4;

CREATE TABLE platform_users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  soccer_user_id BIGINT UNSIGNED NULL,
  username_snapshot VARCHAR(255) NOT NULL,
  nickname_snapshot VARCHAR(255) NOT NULL,
  status ENUM('active', 'disabled') NOT NULL DEFAULT 'active',
  last_login_at DATETIME NULL,
  active_session_id VARCHAR(43) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY platform_users_soccer_user_id_unique (soccer_user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE rooms (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(32) NOT NULL,
  name VARCHAR(64) NOT NULL,
  region VARCHAR(32) NOT NULL,
  hub_name VARCHAR(64) NOT NULL,
  subnet_cidr VARCHAR(32) NOT NULL,
  ip_start VARCHAR(15) NOT NULL,
  ip_end VARCHAR(15) NOT NULL,
  capacity SMALLINT UNSIGNED NOT NULL DEFAULT 100,
  status ENUM('open', 'maintenance', 'closed') NOT NULL DEFAULT 'open',
  sort_order INT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY rooms_code_unique (code),
  UNIQUE KEY rooms_hub_name_unique (hub_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE room_ip_leases (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  room_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  session_id VARCHAR(43) NULL,
  virtual_ip VARCHAR(15) NOT NULL,
  state ENUM('allocated', 'connected', 'released') NOT NULL DEFAULT 'allocated',
  vpn_username VARCHAR(96) NOT NULL,
  real_ip VARCHAR(45) NULL,
  credential_expires_at DATETIME NOT NULL,
  released_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY room_ip_leases_room_ip_unique (room_id, virtual_ip),
  UNIQUE KEY room_ip_leases_active_user (room_id, user_id),
  CONSTRAINT room_ip_leases_room_id_foreign FOREIGN KEY (room_id) REFERENCES rooms (id),
  CONSTRAINT room_ip_leases_user_id_foreign FOREIGN KEY (user_id) REFERENCES platform_users (id),
  KEY room_ip_leases_user_id_index (user_id),
  KEY room_ip_leases_state_index (state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO rooms (code, name, region, hub_name, subnet_cidr, ip_start, ip_end, capacity, sort_order) VALUES
  ('room-01', '对战房间 01', '主节点', 'we8-room-01', '10.222.1.0/24', '10.222.1.10', '10.222.1.109', 100, 1),
  ('room-02', '对战房间 02', '主节点', 'we8-room-02', '10.222.2.0/24', '10.222.2.10', '10.222.2.109', 100, 2),
  ('room-03', '对战房间 03', '主节点', 'we8-room-03', '10.222.3.0/24', '10.222.3.10', '10.222.3.109', 100, 3),
  ('room-04', '对战房间 04', '主节点', 'we8-room-04', '10.222.4.0/24', '10.222.4.10', '10.222.4.109', 100, 4),
  ('room-05', '对战房间 05', '主节点', 'we8-room-05', '10.222.5.0/24', '10.222.5.10', '10.222.5.109', 100, 5),
  ('room-06', '对战房间 06', '主节点', 'we8-room-06', '10.222.6.0/24', '10.222.6.10', '10.222.6.109', 100, 6);

# age recipients that can decrypt the machines' age keys (and thus all vars).
[
  # YubiKey PIV key (same key as in ssh-keys.nix). age can't parse ECDSA SSH
  # keys, so it is given in age-plugin-yubikey's encoding.
  "age1yubikey1qgj0qprapgs3z0h4yzuflwz3qpsqpm9hllu0hqsu2eekgd4re0vkvcuxjgs"
]

# CSV Import Rules

`cmd/import_customer_items` tool uchun qat'iy qoida:

- `1 item = 1 customer`
- agar item allaqachon boshqa customerga ulangan bo'lsa, import **to'xtashi kerak**
- agent yoki tool existing customer link ustiga boshqa customer yozmasligi kerak
- overlap aniqlansa foydalanuvchidan alohida tasdiq olinmaguncha import davom etmaydi

Import semantics:

- CSV dagi har bir unikal nom item code va item name sifatida olinadi
- default `stock_uom = Kg`
- default `item_group = Tayyor mahsulot`
- target customer mavjud bo'lishi shart

Safety policy:

- import mavjud itemni boshqa customerga qo'shib yubormaydi
- import existing customer assignmentni almashtirmaydi
- exclusive reassignment faqat foydalanuvchi alohida, aniq buyruq berganda bajariladi

Current implementation:

- overlap check `internal/importitems/importitems.go` ichida ishlaydi
- conflict bo'lsa tool error beradi va assign/create qilmaydi

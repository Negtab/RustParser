fn main() {
    let numbers = [3, 7, 12, 0, 25, 4];
    let mut total = 0;

    for n in numbers {
        match n % 3 {
            0 => {
                if n > 10 {
                    total += n;
                }
            }
            1 => {
                if n < 5 {
                    total -= 1;
                }
            }
            _ => total += 2,
        }

        if n == 0 {
            println!("zero");
        }
    }

    if total > 20 {
        println!("total = {}", total);
    }
}
